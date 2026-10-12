//go:build !tinygo && !js

package liveplay

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/engine"
)

func TestRetirementBackpressureRetainsEveryEngineWithoutCallbackAllocations(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var first, releaseOnce sync.Once
	var closed atomic.Int32
	pool := newEngineRetirement(func(e *engine.Engine) { first.Do(func() { close(entered); <-release }); _ = e.Close(); closed.Add(1) })
	shutdown := false
	defer func() {
		if !shutdown {
			releaseOnce.Do(func() { close(release) })
			pool.shutdown()
		}
	}()
	engines := make([]*engine.Engine, retiredEngineCapacity)
	for i := range engines {
		e, err := engine.New(engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 32, MaxVoices: 32})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.EnableParallel(0); err != nil {
			t.Fatal(err)
		}
		engines[i] = e
		if !pool.register(e) {
			t.Fatal("registry rejected admitted engine")
		}
	}
	index := 0
	if allocs := testing.AllocsPerRun(10, func() {
		if !pool.retire(engines[index]) {
			panic("retirement rejected")
		}
		index++
	}); allocs != 0 {
		t.Fatalf("callback retirement allocated %g", allocs)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reaper never started")
	}
	for ; index < len(engines); index++ {
		if !pool.retire(engines[index]) {
			t.Fatal("bounded queue lost retirement ownership")
		}
	}
	extra, err := engine.New(engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	if pool.register(extra) {
		t.Fatal("slow cleanup did not backpressure admission")
	}
	for _, e := range engines {
		if pool.find(e) == nil {
			t.Fatal("retired engine lost before Close completed")
		}
	}
	releaseOnce.Do(func() { close(release) })
	pool.shutdown()
	shutdown = true
	if closed.Load() != retiredEngineCapacity {
		t.Fatalf("closed%d engines,want%d", closed.Load(), retiredEngineCapacity)
	}
	for _, e := range engines {
		if pool.find(e) != nil {
			t.Fatal("closed engine retained registry ownership")
		}
	}
}

func TestHundredNativeReplacementsKeepTelemetrySafeAndReaperDrains(t *testing.T) {
	initial, runtime := liveStreamingFixture(t)
	player, err := New(initial, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	if err := player.PlayRange(PlaybackRange{StartTick: 0, EndTick: 1, Loop: true}); err != nil {
		t.Fatal(err)
	}
	stopped, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stopped:
				return
			default:
				_ = initial.Engine.StreamStats()
				_ = initial.Engine.StreamError()
				_ = player.StreamStats()
				_ = player.StreamError()
			}
		}
	}()
	defer func() { close(stopped); <-done }()
	var pcm [128 * 8]byte
	for i := 0; i < 100; i++ {
		next := meterScore(t, "replacement", -6)
		// Ordinary single-track replacement here keeps the test cheap; the
		// initial streamed engine exercises held telemetry while reaped.
		if err := player.Offer(next); err != nil {
			t.Fatal(err)
		}
		if _, err := player.Read(pcm[:]); err != nil {
			t.Fatal(err)
		}
	}
	player.Close()
	if runtime.closes.Load() != 1 {
		t.Fatalf("initial stream closed%d times", runtime.closes.Load())
	}
	select {
	case <-player.retirement.done:
	default:
		t.Fatal("retirement reaper still running after Close")
	}
}
