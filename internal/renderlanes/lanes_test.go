package renderlanes

import (
	"fmt"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobsAndLanes(t *testing.T) {
	if got := Jobs(3); got != 3 {
		t.Fatalf("Jobs(3) = %d", got)
	}
	if got := Jobs(1000); got != MaxLanes {
		t.Fatalf("Jobs(1000) = %d, want %d", got, MaxLanes)
	}
	if got, want := Jobs(0), min(runtime.GOMAXPROCS(0), MaxLanes); got != want {
		t.Fatalf("Jobs(0) = %d, want GOMAXPROCS %d", got, want)
	}
	for _, c := range []struct{ jobs, tracks, want int }{{1, 12, 1}, {4, 12, 4}, {8, 3, 3}, {4, 1, 1}, {4, 0, 1}, {100, 100, MaxLanes}} {
		if got := Lanes(c.jobs, c.tracks); got != c.want {
			t.Errorf("Lanes(%d, %d) = %d, want %d", c.jobs, c.tracks, got, c.want)
		}
	}
	if New(1, 8, func(int, int, int) {}) != nil || New(4, 1, func(int, int, int) {}) != nil {
		t.Fatal("a pool with one lane or one track was created")
	}
}

// Every track renders exactly once per segment, with that segment's frames,
// whatever the lane count and the cost of each track.
func TestRunRendersEveryTrackOnce(t *testing.T) {
	const tracks = 11
	var calls [tracks]atomic.Int64
	var lastStart, lastCount [tracks]atomic.Int64
	p := New(4, tracks, func(track, start, count int) {
		calls[track].Add(1)
		lastStart[track].Store(int64(start))
		lastCount[track].Store(int64(count))
		// Make track 0 the heaviest by far, as an electric piano is.
		spin := time.Duration(count) * 20 * time.Nanosecond
		if track == 0 {
			spin *= 20
		}
		for deadline := time.Now().Add(spin); time.Now().Before(deadline); {
		}
	})
	defer p.Close()
	for segment := range 50 {
		start, count := segment%7, 64+segment*13
		p.Run(start, count)
		for track := range tracks {
			if got := calls[track].Load(); got != int64(segment+1) {
				t.Fatalf("segment %d: track %d rendered %d times", segment, track, got)
			}
			if lastStart[track].Load() != int64(start) || lastCount[track].Load() != int64(count) {
				t.Fatalf("segment %d: track %d got frames %d+%d", segment, track, lastStart[track].Load(), lastCount[track].Load())
			}
		}
	}
	stats := p.Stats()
	if stats.Segments != 50 || stats.Frames == 0 {
		t.Fatalf("stats %+v", stats)
	}
	if runtime.GOMAXPROCS(0) >= 2 && stats.ParallelSegments == 0 {
		t.Fatalf("no segment ran on more than one lane: %+v", stats)
	}
	// Once measured, the heavy track has a lane to itself.
	if p.used > 1 {
		for lane := 0; lane < p.used; lane++ {
			tracksOnLane := p.order[p.bounds[lane]:p.bounds[lane+1]]
			if slices.Contains(tracksOnLane, 0) && len(tracksOnLane) != 1 {
				t.Fatalf("the heaviest track shares lane %d with %v", lane, tracksOnLane)
			}
		}
	}
}

// A segment too short to pay for a hand-off runs on the caller alone.
func TestLightSegmentsStaySerial(t *testing.T) {
	var calls atomic.Int64
	p := New(4, 4, func(int, int, int) { calls.Add(1) })
	defer p.Close()
	for range 20 {
		p.Run(0, 64)
	}
	if calls.Load() != 80 {
		t.Fatalf("calls = %d", calls.Load())
	}
	// The first segment uses the default estimate; after it every track
	// measures as nearly free, so later segments stay on the caller.
	if stats := p.Stats(); stats.ParallelSegments > 1 {
		t.Fatalf("light segments ran on lanes: %+v", stats)
	}
}

func TestPanicInALaneReachesTheCaller(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two processors to run a worker lane")
	}
	p := New(2, 2, func(track, start, count int) {
		if track == 1 && start == 5 {
			panic("track 1 failed")
		}
		for deadline := time.Now().Add(100 * time.Microsecond); time.Now().Before(deadline); {
		}
	})
	defer p.Close()
	p.Run(0, 4096) // measure both tracks as heavy enough for two lanes
	defer func() {
		if value := recover(); value != "track 1 failed" {
			t.Fatalf("recovered %v", value)
		}
		if busy.Load() != 0 {
			t.Fatalf("the failed segment kept %d lanes of the process budget", busy.Load())
		}
	}()
	p.Run(5, 4096)
	t.Fatal("Run returned after a lane panicked")
}

// Two pools at once never run more worker lanes than the process has
// processors to spare.
func TestBudgetLimitsConcurrentPools(t *testing.T) {
	limit := runtime.GOMAXPROCS(0) - 1
	var peak, running atomic.Int64
	render := func(int, int, int) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		for deadline := time.Now().Add(50 * time.Microsecond); time.Now().Before(deadline); {
		}
		running.Add(-1)
	}
	a, b := New(8, 8, render), New(8, 8, render)
	defer a.Close()
	defer b.Close()
	done := make(chan struct{})
	go func() {
		for range 40 {
			b.Run(0, 4096)
		}
		close(done)
	}()
	for range 40 {
		a.Run(0, 4096)
	}
	<-done
	// Each Run's caller renders a lane of its own besides the budget.
	if got := peak.Load(); got > int64(limit+2) {
		t.Fatalf("%d lanes ran at once; the budget allows %d workers and two callers", got, limit)
	}
	if busy.Load() != 0 {
		t.Fatalf("budget not returned: %d", busy.Load())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	p := New(3, 3, func(int, int, int) {})
	p.Run(0, 128)
	p.Close()
	p.Close()
	var nilPool *Pool
	nilPool.Close()
	if nilPool.Lanes() != 1 {
		t.Fatal("a nil pool must report one lane")
	}
}

// BenchmarkHandoff measures waking the worker lanes for one segment of
// trivial work and joining them: the overhead each parallel segment pays.
// MinLaneWork is set well above it.
func BenchmarkHandoff(b *testing.B) {
	for _, lanes := range []int{2, 4} {
		b.Run(fmt.Sprintf("lanes=%d", lanes), func(b *testing.B) {
			p := New(lanes, lanes, func(int, int, int) {})
			defer p.Close()
			b.ResetTimer()
			for range b.N {
				// Pretend every track is heavy so each segment uses every lane.
				for track := range p.cost {
					p.cost[track], p.measured[track] = 1e6, true
				}
				p.Run(0, 4096)
			}
			b.StopTimer()
			if p.Stats().MaxUsed != min(lanes, runtime.GOMAXPROCS(0)) {
				b.Fatalf("used %d lanes", p.Stats().MaxUsed)
			}
		})
	}
}
