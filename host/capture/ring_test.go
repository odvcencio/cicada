package capture

import (
	"sync"
	"testing"
)

func TestRingCopiesRawInputAndAccountsForGaps(t *testing.T) {
	r, err := NewRing(2, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	b := Block{Frames: 4, Layout: LayoutStereo, DeviceEpoch: 7}
	in := [][]float32{{1, 2, 3, 4}, {-1, -2, -3, -4}}
	if !r.Push(b, in) || !r.Push(b, in) || r.Push(b, in) || r.Push(b, in) {
		t.Fatal("ring did not enforce capacity")
	}
	in[0][0] = 99
	r.Consume(func(got Block, pcm [][]float32) {
		if pcm[0][0] != 1 || pcm[1][3] != -4 || got.DeviceEpoch != 7 {
			t.Fatalf("borrowed input was retained: %v, %+v", pcm, got)
		}
	})
	if !r.Push(b, in) {
		t.Fatal("ring did not reuse consumed slot")
	}
	r.Consume(func(Block, [][]float32) {})
	r.Consume(func(got Block, _ [][]float32) {
		if got.GapFrames != 8 || got.Flags&QueueOverrun == 0 {
			t.Fatalf("missing gap: %+v", got)
		}
	})
	if got := r.Stats(); got.Overruns != 2 || got.LostFrames != 8 {
		t.Fatalf("stats = %+v", got)
	}
}

func TestRingCallbackAllocationsIncludingOverrunAndInvalidInput(t *testing.T) {
	r, _ := NewRing(1, 4, 1)
	space, _ := NewRing(1002, 4, 1)
	b := Block{Frames: 4, Layout: LayoutMono}
	in := [][]float32{{1, 2, 3, 4}}
	for _, test := range []struct {
		name string
		push func()
	}{
		{"normal", func() { space.Push(b, in) }},
		{"overrun", func() { r.Push(b, in) }},
		{"invalid", func() { r.Push(b, nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(1000, test.push); n != 0 {
				t.Fatalf("callback allocations = %g", n)
			}
		})
	}
	if r.Stats().LostFrames == 0 || r.Stats().InvalidBlocks == 0 {
		t.Fatal("trailing losses were not counted")
	}
}

func TestRingConcurrentProducerAndWriter(t *testing.T) {
	r, _ := NewRing(8, 1, 1)
	const blocks = 10000
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for i := 0; i < blocks; i++ {
			r.Push(Block{Frames: 1, Layout: LayoutMono, DeviceFrame: uint64(i)}, [][]float32{{float32(i)}})
		}
	}()
	finished := make(chan struct{})
	go func() { done.Wait(); close(finished) }()
	accepted := uint64(0)
	consume := func(b Block, pcm [][]float32) {
		accepted++
		if pcm[0][0] != float32(b.DeviceFrame) {
			t.Error("published slot was overwritten")
		}
	}
	for {
		if r.Consume(consume) {
			continue
		}
		select {
		case <-finished:
			for r.Consume(consume) {
			}
			if accepted+r.Stats().Overruns != blocks {
				t.Fatalf("accepted %d + overruns %+v != %d", accepted, r.Stats(), blocks)
			}
			return
		default:
		}
	}
}
