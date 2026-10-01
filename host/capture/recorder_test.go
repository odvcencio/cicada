package capture

import (
	"errors"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/seq"
)

func TestCountInDefaultAndIntegerClock(t *testing.T) {
	c, err := DefaultCountIn(48000, 120000)
	if err != nil || c.Frames != 96000 || c.count != 4 {
		t.Fatalf("default count-in = %+v, %v", c, err)
	}
	c, err = DefaultCountIn(44100, 137333)
	clock, _ := seq.NewClock(44100, 137333)
	if err != nil || c.Frames != clock.SampleAtTick(seq.TicksPerBar) {
		t.Fatalf("fractional tempo count-in = %+v, %v", c, err)
	}
	if _, err := NewCountIn(48000, 120000, 9); err == nil {
		t.Fatal("accepted unbounded count-in")
	}
}

func TestRecorderTrailingOverrunAndWriterDrain(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r, err := NewRecorder(1, 4, 1, func(b RecordedBlock, input [][]float32) error {
		close(entered)
		<-release
		if b.RawFrame != 0 || input[0][0] != 0.75 {
			t.Errorf("raw capture changed: %+v, %v", b, input)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewCountIn(48000, 120000, 0)
	if err := r.Begin(c, Calibration{}); err != nil {
		t.Fatal(err)
	}
	b := Block{DeviceEpoch: 1, SampleRate: 48000, Frames: 4, Layout: LayoutMono}
	input := [][]float32{{0.75, 0.5, -0.5, -0.75}}
	r.Capture(b, input)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not consume")
	}
	b.DeviceFrame = 4
	r.Capture(b, input)
	b.DeviceFrame = 8
	r.Capture(b, input)
	if got := r.Snapshot(); !got.Incomplete || got.Ring.Overruns != 2 || got.Ring.LostFrames != 8 {
		t.Fatalf("silent overrun: %+v", got)
	}
	close(release)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got := r.Snapshot(); got.TrailingGap == nil || got.TrailingGap.RawFrame != 12 || got.TrailingGap.Timing.GapFrames != 8 || got.TrailingGap.Timing.Flags&QueueOverrun == 0 {
		t.Fatalf("trailing gap not exposed after drain: %+v", got.TrailingGap)
	}
	if got := r.Snapshot(); got.WrittenFrames != 4 || !got.Incomplete {
		t.Fatalf("drain = %+v", got)
	}
}

func TestRecorderWriterFailureAndDeviceGap(t *testing.T) {
	want := errors.New("simulated storage full")
	var records []RecordedBlock
	r, _ := NewRecorder(4, 4, 1, func(b RecordedBlock, _ [][]float32) error { records = append(records, b); return want })
	c, _ := NewCountIn(48000, 120000, 0)
	r.Begin(c, Calibration{})
	for _, frame := range []uint64{0, 12} {
		r.Capture(Block{EngineEpoch: 1, DeviceEpoch: 1, DeviceFrame: frame, SampleRate: 48000, Frames: 4, Layout: LayoutMono}, [][]float32{{1, 2, 3, 4}})
	}
	if err := r.Close(); err != want {
		t.Fatalf("storage error = %v", err)
	}
	if len(records) != 2 || records[1].RawFrame != 12 || records[1].Timing.GapFrames != 8 || records[1].Timing.Flags&DeviceDiscontinuity == 0 {
		t.Fatalf("missing device gap: %+v", records)
	}
	if got := r.Snapshot(); !got.Incomplete || got.Error != want.Error() || got.WrittenFrames != 0 {
		t.Fatalf("storage failure not visible: %+v", got)
	}
}

func TestRecorderCallbackAllocations(t *testing.T) {
	// Isolate the producer from allocations in an application-supplied writer.
	ring, _ := NewRing(1002, 256, 2)
	r := &Recorder{ring: ring}
	c, _ := DefaultCountIn(48000, 120000)
	r.Begin(c, Calibration{})
	in, out := [][]float32{make([]float32, 256), make([]float32, 256)}, [][]float32{make([]float32, 256), make([]float32, 256)}
	b := Block{DeviceEpoch: 1, SampleRate: 48000, Period: 256, Frames: 256, Layout: LayoutStereo}
	if n := testing.AllocsPerRun(1000, func() { r.Capture(b, in); r.LeadIn(out) }); n != 0 {
		t.Fatalf("callback allocations = %g", n)
	}
	if err := r.Begin(c, Calibration{}); err == nil {
		t.Fatal("overwrote retained take")
	}
}

func TestRecorderConsumedGapIsNotTrailing(t *testing.T) {
	entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var last RecordedBlock
	r, err := NewRecorder(1, 4, 1, func(b RecordedBlock, _ [][]float32) error {
		if b.RawFrame == 0 {
			close(entered)
			<-release
		}
		last = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, _ := NewCountIn(48000, 120000, 0)
	if err := r.Begin(c, Calibration{}); err != nil {
		t.Fatal(err)
	}
	b := Block{DeviceEpoch: 1, EngineEpoch: 1, SampleRate: 48000, Frames: 4, Layout: LayoutMono}
	pcm := [][]float32{{1, 2, 3, 4}}
	r.Capture(b, pcm)
	<-entered
	b.DeviceFrame = 4
	r.Capture(b, pcm)
	close(release)
	go func() {
		for r.ring.read.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("ring did not drain")
	}
	b.DeviceFrame = 8
	r.Capture(b, pcm)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if last.RawFrame != 8 || last.Timing.GapFrames != 4 || r.Snapshot().TrailingGap != nil {
		t.Fatalf("consumed gap was duplicated: last=%+v snapshot=%+v", last, r.Snapshot())
	}
}
