//go:build !tinygo

package engine

import (
	"context"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/stream"
)

type testStreamFactory struct {
	runtime   *testStreamRuntime
	autoPrime bool
}
type testStreamRuntime struct {
	cache           *stream.Cache
	readers         []*stream.Reader
	retired, closed bool
	autoPrime       bool
}

func (f *testStreamFactory) New(readers, rate, block int) (StreamingRuntime, error) {
	cache, err := stream.New(stream.Config{Readers: readers, Pages: readers * 6, AheadPages: 3}, []stream.Asset{{ID: 1, Frames: 500000, RateHz: 96000, Channels: 2}})
	if err != nil {
		return nil, err
	}
	r := &testStreamRuntime{cache: cache, autoPrime: f.autoPrime}
	for range readers {
		reader, err := cache.NewReader(1)
		if err != nil {
			return nil, err
		}
		reader.Stop()
		r.readers = append(r.readers, reader)
	}
	f.runtime = r
	return r, nil
}
func (r *testStreamRuntime) Reader(i int) *stream.Reader { return r.readers[i] }
func (r *testStreamRuntime) Stats() stream.Stats         { return r.cache.Stats() }
func (r *testStreamRuntime) LastError() error            { return nil }
func (r *testStreamRuntime) Retire()                     { r.retired = true }
func (r *testStreamRuntime) Close() error                { r.closed = true; return nil }
func (r *testStreamRuntime) WaitReady(ctx context.Context, reader *stream.Reader) error {
	if r.autoPrime {
		for {
			work, ok := r.cache.Next()
			if !ok {
				break
			}
			for i := range work.Left() {
				work.Left()[i] = float32(math.Sin(float64(work.Start+int64(i))*.015)) * .05
				work.Right()[i] = -work.Left()[i] * .5
			}
			work.Publish()
		}
	}
	if !reader.WindowReady() {
		return Error("test stream unavailable")
	}
	return ctx.Err()
}
func streamedEngineFixture(t *testing.T, prime bool) (*Engine, *testStreamRuntime) {
	t.Helper()
	factory := &testStreamFactory{autoPrime: prime}
	cfg := Config{SampleRate: 48000, MaxBlock: 4096, Tracks: 1, MaxVoices: 32, BPMMilli: 120000, Streaming: factory}
	cfg.Track[0] = TrackConfig{Kind: VoiceAudio, GainSet: true}
	cfg.Assets = []AudioAsset{{StreamID: 1, Frames: 500000, SampleRate: 96000}}
	cfg.Clips = []ClipConfig{{StartFrame: 100, EndFrame: 400000}}
	cfg.Schedule = []ScheduleEvent{{EndTick: 15360, Kind: ScheduleClip, ID: 1}, {Tick: 15360, EndTick: 15360, Kind: ScheduleClipEnd, ID: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e, factory.runtime
}
func TestStreamedClipStarvationAdvancesCountsAndRecovers(t *testing.T) {
	e, runtime := streamedEngineFixture(t, false)
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var l, r [128]float32
	e.Render(l[:], r[:])
	stats := e.StreamStats()
	if stats.Misses != 1 || stats.MissingFrames != 128 || stats.PageLookups != 1 || e.transport.Sample() != 128 {
		t.Fatalf("missing PCM stalled or invisible: %+v sample%d", stats, e.transport.Sample())
	}
	for _, v := range l {
		if v != 0 {
			t.Fatal("unavailable initial PCM must be silent")
		}
	}
	runtime.autoPrime = true
	if _, err := e.PrepareRender(context.Background(), 128); err != nil {
		t.Fatal(err)
	}
	e.Render(l[:], r[:])
	stats = e.StreamStats()
	if stats.Recoveries != 1 || stats.MissingFrames != 128 || e.transport.Sample() != 256 {
		t.Fatalf("recovery failed: %+v", stats)
	}
	peak := float32(0)
	for _, v := range l {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	if peak == 0 {
		t.Fatal("recovered clip remained silent")
	}
}
func TestStreamedClipMissingFIRTapsStayBoundedAndAllocationFree(t *testing.T) {
	e, _ := streamedEngineFixture(t, false)
	var left, right [4096]float32
	var message cmd.Message
	allocs := testing.AllocsPerRun(30, func() {
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg1: 163})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.Render(left[:], right[:])
		for e.Poll(&message) {
		}
	})
	if allocs != 0 || e.faulted {
		t.Fatalf("streamed callback allocates: %g fault%v", allocs, e.faulted)
	}
	// Two-to-one SRC spans three source pages for a4096frame callback, even
	// when its192tap FIR straddles a missing-page boundary on many samples.
	if stats := e.StreamStats(); stats.PageLookups > 31*4 {
		t.Fatalf("FIR multiplied missing page scans: %+v", stats)
	}
}
func TestStreamedRetireAndCloseReleaseAllReaders(t *testing.T) {
	e, runtime := streamedEngineFixture(t, true)
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	if _, err := e.PrepareRender(context.Background(), 128); err != nil {
		t.Fatal(err)
	}
	var l, r [128]float32
	e.Render(l[:], r[:])
	if allocs := testing.AllocsPerRun(10, func() { e.Retire() }); allocs != 0 {
		t.Fatalf("retirement allocates: %g", allocs)
	}
	if !runtime.retired {
		t.Fatal("runtime cancellation not requested")
	}
	for i := range runtime.readers {
		if !runtime.readers[i].WindowReady() {
			t.Fatal("retired reader retains demand")
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if !runtime.closed {
		t.Fatal("host Close did not join runtime")
	}
}
