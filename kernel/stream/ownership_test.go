package stream

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A reader can see ready, get descheduled, and increment only after a worker
// has claimed the slot. Publication must retain that unsuccessful probe until
// its release, including when another reader acquires the new generation.
func TestPublicationPreservesLateReaderProbe(t *testing.T) {
	c, readers := newTestCache(t, 1, 0, PageFrames*3)
	readers[0].SeekFrame(0, 1)
	w, ok := c.Next()
	if !ok {
		t.Fatal("no work")
	}
	w.page.state.Add(pin)
	if c.Ready(1, 0) {
		t.Fatal("unpublished PCM reported ready")
	}
	fill(w)
	if !w.Publish() || w.page.state.Load() != ready+pin {
		t.Fatal("publication erased a late pin")
	}
	p := c.acquire(1, 0)
	if p == nil || p.state.Load() != ready+2*pin {
		t.Fatal("new generation could not be pinned")
	}
	w.page.state.Add(^uint32(pin - 1))
	if p.state.Load() != ready+pin || p.left[GuardFrames] != pcm(0) {
		t.Fatal("late release changed a new reader's page")
	}
	p.state.Add(^uint32(pin - 1))
	if p.state.Load() != ready {
		t.Fatal("publication leaked its probe count")
	}
}

func TestCancelledAndFailedFillsPreserveLateProbesAcrossReuse(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "cancel"
		if failed {
			name = "fail"
		}
		t.Run(name, func(t *testing.T) {
			c, readers := newTestCache(t, 1, 0, PageFrames*3)
			readers[0].SeekFrame(0, 1)
			w, ok := c.Next()
			if !ok {
				t.Fatal("no work")
			}
			w.page.state.Add(pin)
			if failed {
				w.Fail()
			} else {
				w.Cancel()
			}
			if w.page.state.Load() != empty+pin || c.Ready(1, 0) {
				t.Fatal("aborted fill erased its probe or exposed PCM")
			}
			next, ok := c.Next()
			if !ok || next.page != w.page || next.Index != 0 {
				t.Fatal("transient empty-slot pins prevented reuse")
			}
			fill(next)
			if !next.Publish() {
				t.Fatal("replacement fill was cancelled")
			}
			p := c.acquire(1, 0)
			if p == nil {
				t.Fatal("replacement publication was missing")
			}
			w.page.state.Add(^uint32(pin - 1))
			if p.state.Load() != ready+pin {
				t.Fatal("late release erased replacement ownership")
			}
			p.state.Add(^uint32(pin - 1))
			if p.state.Load() != ready {
				t.Fatal("replacement left a pin behind")
			}
		})
	}
}

func TestUnpublishedSlotWithProbeStillCountsOneBlockMiss(t *testing.T) {
	c, readers := newTestCache(t, 1, 0, PageFrames*3)
	r := readers[0]
	r.SeekFrame(0, 1)
	w, ok := c.Next()
	if !ok {
		t.Fatal("no work")
	}
	w.page.state.Add(pin)
	for i := range w.Left() {
		w.Left()[i] = 1
		w.Right()[i] = 1
	}
	var left, right [128]float32
	r.Render(left[:], right[:])
	if left != [128]float32{} || right != [128]float32{} {
		t.Fatal("reader exposed a fill in progress")
	}
	if s := r.Stats(); s.Misses != 1 || s.PageLookups != 1 || s.MissingFrames != 128 {
		t.Fatal("unpublished lookup repeated inside the callback", s)
	}
	fill(w)
	w.Publish()
	w.page.state.Add(^uint32(pin - 1))
	r.Render(left[:], right[:])
	if s := r.Stats(); s.Recoveries != 1 || s.PageLookups != 2 {
		t.Fatal("publication did not recover on the next block", s)
	}
	r.Stop()
}

func TestConcurrentWorkerAndReadersKeepPCMGenerationOwnership(t *testing.T) {
	const lanes = 8
	c, err := New(Config{Pages: lanes * 3, Readers: lanes, AheadPages: 0}, []Asset{
		{ID: 1, Frames: PageFrames * 32, RateHz: 48000, Channels: 2},
		{ID: 2, Frames: PageFrames * 32, RateHz: 48000, Channels: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	var readers [lanes]*Reader
	for i := range readers {
		readers[i], _ = c.NewReader(1)
		readers[i].SeekFrame(0, 1)
	}
	value := func(asset uint32, frame int64) float32 { return float32(int64(asset)*1000000 + frame) }
	fillGeneration := func(w Work) {
		for i := range w.Left() {
			v := value(w.Asset.ID, w.Start+int64(i))
			w.Left()[i], w.Right()[i] = v, -v
		}
	}
	for {
		w, ok := c.Next()
		if !ok {
			break
		}
		fillGeneration(w)
		w.Publish()
	}
	stop := make(chan struct{})
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if w, ok := c.Next(); ok {
				fillGeneration(w)
				runtime.Gosched()
				w.Publish()
			} else {
				runtime.Gosched()
			}
		}
	}()
	var bad, good atomic.Int64
	var renderers sync.WaitGroup
	deadline := time.Now().Add(10 * time.Second)
	for lane, r := range readers {
		renderers.Add(1)
		go func(lane int, r *Reader) {
			defer renderers.Done()
			defer r.Stop()
			for i := 0; i < 3000; i++ {
				id := uint32(1 + i%2)
				frame := int64((i*7+lane)%31)*PageFrames + int64(i%128)
				r.SetAsset(id)
				r.SeekFrame(frame, 1)
				// Keep occasional demand stable through publication. Otherwise
				// every fill can be cancelled before a busy worker publishes,
				// leaving the eviction path untested despite correct PCM.
				if i%64 == 0 {
					for !r.WindowReady() {
						if time.Now().After(deadline) {
							t.Error("worker did not publish a stable reader window")
							return
						}
						runtime.Gosched()
					}
				}
				l, rr, ok := r.ReadFrame(frame)
				if ok {
					good.Add(1)
					if l != value(id, frame) || rr != -l {
						bad.Add(1)
					}
					// Retire demand while retaining the guard. The worker may recycle
					// other slots but cannot change this reader's held generation.
					r.AdvanceFrame((frame + PageFrames*13) % (PageFrames * 31))
					runtime.Gosched()
					l2, r2, ok2 := r.ReadFrame(frame)
					if !ok2 || l2 != l || r2 != rr {
						bad.Add(1)
					}
				} else {
					runtime.Gosched()
				}
			}
		}(lane, r)
	}
	renderers.Wait()
	close(stop)
	worker.Wait()
	if bad.Load() != 0 || good.Load() == 0 || c.Stats().Evicted == 0 {
		t.Fatalf("generation ownership: corrupted=%d read=%d stats=%+v", bad.Load(), good.Load(), c.Stats())
	}
	for i := range c.pages {
		if c.pages[i].state.Load() != empty && c.pages[i].state.Load() != ready {
			t.Fatal("retired readers leaked a page pin")
		}
	}
}
