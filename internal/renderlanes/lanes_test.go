package renderlanes

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobsAndLanes(t *testing.T) {
	// Zero is serial: a caller that never asks for lanes gets none.
	if got := Jobs(0); got != 1 {
		t.Fatalf("Jobs(0) = %d, want the serial render", got)
	}
	if got, want := Jobs(1), 1; got != want {
		t.Fatalf("Jobs(1) = %d", got)
	}
	if got, want := Jobs(3), min(3, runtime.NumCPU()); got != want {
		t.Fatalf("Jobs(3) = %d, want %d", got, want)
	}
	if got, want := Jobs(1000), min(runtime.NumCPU(), MaxLanes); got != want {
		t.Fatalf("Jobs(1000) = %d, want the CPU count %d", got, want)
	}
	cpus := min(runtime.NumCPU(), runtime.GOMAXPROCS(0))
	if got, want := Jobs(AutoJobs), max(1, min(AutoCap, cpus/2)); got != want || got > AutoCap {
		t.Fatalf("Jobs(AutoJobs) = %d, want %d (at most %d, half of %d CPUs)", got, want, AutoCap, cpus)
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

// Every track renders every frame of every segment exactly once, in
// consecutive runs, whatever the lane count and the cost of each track; the
// caller sees a frame only after every lane has published it.
func TestRunRendersEveryTrackOnce(t *testing.T) {
	const tracks = 11
	// next[track] is the next frame the track must render; a lane writes only
	// its own tracks' entries, and the caller reads them after Ready.
	var next [tracks]atomic.Int64
	var overlap atomic.Bool
	p := New(4, tracks, func(track, from, count int) {
		if next[track].Load() != int64(from) {
			overlap.Store(true)
		}
		next[track].Store(int64(from + count))
		// Make track 0 the heaviest by far, as an electric piano is.
		spin := time.Duration(count) * 20 * time.Nanosecond
		if track == 0 {
			spin *= 20
		}
		for deadline := time.Now().Add(spin); time.Now().Before(deadline); {
		}
	})
	defer p.Close()
	// The costs the spin loop imitates, stated so that a pause cannot make
	// the heaviest track or the mix dominate and keep every segment serial:
	// from 130 frames on, a segment is worth two lanes.
	stateCosts(p, func(track int) float64 {
		if track == 0 {
			return 400
		}
		return 20
	})
	for segment := range 50 {
		start, count := segment%7, 64+segment*13
		for track := range next {
			next[track].Store(int64(start))
		}
		p.Begin(start, count)
		for frame := start; frame < start+count; frame++ {
			ready := p.Ready(frame)
			if ready <= frame || ready > start+count {
				t.Fatalf("segment %d: Ready(%d) = %d", segment, frame, ready)
			}
			for track := range next {
				if next[track].Load() < int64(frame+1) {
					t.Fatalf("segment %d: frame %d of track %d was not rendered before Ready returned", segment, frame, track)
				}
			}
		}
		p.End()
		for track := range next {
			if got := next[track].Load(); got != int64(start+count) {
				t.Fatalf("segment %d: track %d rendered up to %d, want %d", segment, track, got, start+count)
			}
		}
		if overlap.Load() {
			t.Fatalf("segment %d: a track's runs were not consecutive", segment)
		}
	}
	stats := p.Stats()
	if stats.Segments != 50 || stats.Frames == 0 {
		t.Fatalf("stats %+v", stats)
	}
	if runtime.GOMAXPROCS(0) >= 2 && stats.ParallelSegments == 0 {
		t.Fatalf("no segment ran on more than one lane: %+v", stats)
	}
}

// With fixed costs, LPT gives the heaviest track a lane of its own and
// counts the caller's mix as work already on lane 0.
func TestAssignBalancesByCost(t *testing.T) {
	p := New(4, 8, func(int, int, int) {})
	defer p.Close()
	for track, cost := range []float64{900, 100, 120, 80, 110, 90, 100, 95} {
		p.cost[track], p.measured[track] = cost, true
	}
	p.mixCost, p.mixMeasured = 300, true
	p.assign(4)
	loads := make([]float64, 4)
	for lane := 0; lane < 4; lane++ {
		tracks := p.order[p.bounds[lane]:p.bounds[lane+1]]
		if slices.Contains(tracks, 0) && len(tracks) != 1 {
			t.Fatalf("the heaviest track shares lane %d with %v", lane, tracks)
		}
		if lane == 0 {
			loads[lane] = p.mixCost
		}
		for _, track := range tracks {
			loads[lane] += p.cost[track]
		}
	}
	// The other seven tracks (695) and the mix (300) fill three lanes of
	// about 330 each; no lane but the heaviest track's goes far above that.
	for lane, load := range loads {
		if load > 450 && load != 900 {
			t.Fatalf("lane %d carries %g: %v (bounds %v order %v)", lane, load, loads, p.bounds, p.order)
		}
	}
	if total := len(p.order[p.bounds[0]:p.bounds[4]]); total != 8 {
		t.Fatalf("%d tracks assigned, want 8", total)
	}
}

// A segment too short to pay for a hand-off runs on the caller alone.
// stateCosts makes p decide from cost, in nanoseconds per frame (track -1 is
// the caller's mix), from the first segment on, instead of from measured
// time: a test that asserts a scheduling decision must not fail because the
// machine paused during a render.
func stateCosts(p *Pool, cost func(track int) float64) {
	p.statedCost = cost
	for track := range p.cost {
		p.cost[track], p.measured[track] = cost(track), true
	}
	p.mixCost, p.mixMeasured = cost(-1), true
}

func TestLightSegmentsStaySerial(t *testing.T) {
	var calls atomic.Int64
	p := New(4, 4, func(int, int, int) { calls.Add(1) })
	defer p.Close()
	// Four tracks and the mix at 5 ns per frame: 64 frames of all of them
	// (1,600 ns) are far below MinLaneWork, so no segment is worth a lane.
	stateCosts(p, func(int) float64 { return 5 })
	for range 20 {
		p.Run(0, 64)
	}
	if calls.Load() != 80 {
		t.Fatalf("calls = %d, want one 64-frame run per track per segment", calls.Load())
	}
	// Every segment stays on the caller and counts as light
	// (render.SerialLight), not starved.
	if stats := p.Stats(); stats.ParallelSegments != 0 || stats.LightSegments != 20 || stats.StarvedSegments != 0 {
		t.Fatalf("light segments ran on lanes or were not counted as light: %+v", stats)
	}
}

// observe folds a segment's measured nanoseconds into each estimate: the
// first sample sets it, later ones move it by costWeight.
func TestObserveFoldsMeasuredTimes(t *testing.T) {
	p := New(2, 2, func(int, int, int) {})
	defer p.Close()
	p.took[0], p.took[1] = 6400, 640
	p.observe(64, 3200)
	if p.cost[0] != 100 || p.cost[1] != 10 || p.mixCost != 50 || !p.measured[0] || !p.measured[1] || !p.mixMeasured {
		t.Fatalf("first sample: costs %v, mix %g", p.cost, p.mixCost)
	}
	p.took[0], p.took[1] = 12800, 640
	p.observe(64, 0)
	if want := 100 + costWeight*(200-100); p.cost[0] != want || p.cost[1] != 10 || p.mixCost != 50+costWeight*(0-50) {
		t.Fatalf("second sample: costs %v (track 0 want %g), mix %g", p.cost, want, p.mixCost)
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
	// Two equal tracks and no mix: every segment is worth two lanes, and
	// track 1 renders on worker lane 1.
	stateCosts(p, func(track int) float64 {
		if track < 0 {
			return 0
		}
		return 2000
	})
	p.Run(0, 4096)
	defer func() {
		lanePanic, ok := recover().(*LanePanic)
		if !ok || lanePanic.Lane != 1 || lanePanic.Value != "track 1 failed" || !strings.Contains(string(lanePanic.Stack), "TestPanicInALaneReachesTheCaller") {
			t.Fatalf("recovered %#v", lanePanic)
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
	// Stated heavy costs: every segment of both pools wants all eight lanes,
	// so the pools always compete for the budget.
	heavy := func(int) float64 { return 1e4 }
	stateCosts(a, heavy)
	stateCosts(b, heavy)
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

// BenchmarkHandoff measures starting the worker lanes on a one-chunk segment
// of trivial work and joining them: the overhead each parallel segment pays.
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
				p.Run(0, chunkFrames)
			}
			b.StopTimer()
			if p.Stats().MaxUsed != min(lanes, runtime.GOMAXPROCS(0)) {
				b.Fatalf("used %d lanes", p.Stats().MaxUsed)
			}
		})
	}
}

// A panic in the caller's own lane is raised after the worker lanes stop,
// and the Pool keeps working for later segments.
func TestPanicInTheCallersLane(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 2 {
		t.Skip("needs two processors to run a worker lane")
	}
	defer ForceEveryLane()()
	var fail atomic.Bool
	p := New(2, 2, func(track, from, count int) {
		if fail.Load() && from == 0 {
			panic("lane failed")
		}
	})
	defer p.Close()
	fail.Store(true)
	func() {
		defer func() {
			if lanePanic, ok := recover().(*LanePanic); !ok || lanePanic.Value != "lane failed" || len(lanePanic.Stack) == 0 {
				t.Fatalf("recovered %#v", lanePanic)
			}
		}()
		p.Run(0, 1024)
		t.Fatal("Run returned after both lanes panicked")
	}()
	if busy.Load() != 0 || p.active {
		t.Fatalf("after the panic: budget %d, active %v", busy.Load(), p.active)
	}
	fail.Store(false)
	p.Run(0, 1024)
}

// The lane count changes from segment to segment, with very short segments
// between long ones, while a second Pool competes for the process budget.
// Every track must still render each frame exactly once, in order: a worker
// that read a stale lane count would render a segment twice.
func TestLaneCountChangesBetweenSegments(t *testing.T) {
	const tracks = 6
	stress := func(seed int) {
		var next [tracks]atomic.Int64
		var broken atomic.Bool
		p := New(tracks, tracks, func(track, from, count int) {
			if !next[track].CompareAndSwap(int64(from), int64(from+count)) {
				broken.Store(true)
			}
		})
		defer p.Close()
		for segment := range 400 {
			// Alternate the estimated work so worth picks 1 lane, then all.
			heavy := (segment+seed)%3 != 0
			for track := range p.cost {
				p.cost[track], p.measured[track] = 1, true
				if heavy {
					p.cost[track] = 1e6
				}
			}
			// The mix too, or End's measured mix time would decide: a pause
			// there could put a light segment on lanes.
			p.mixCost, p.mixMeasured = 1, true
			start, count := segment%5, 1+(segment*37)%300
			for track := range next {
				next[track].Store(int64(start))
			}
			p.Begin(start, count)
			p.Ready(start + count - 1)
			p.End()
			for track := range next {
				if got := next[track].Load(); got != int64(start+count) || broken.Load() {
					t.Errorf("seed %d segment %d: track %d rendered to %d of %d (runs out of order: %v)", seed, segment, track, got, start+count, broken.Load())
					return
				}
			}
		}
	}
	done := make(chan struct{})
	go func() { stress(1); close(done) }()
	stress(0)
	<-done
	if busy.Load() != 0 {
		t.Fatalf("budget not returned: %d", busy.Load())
	}
}

// Many short segments on every lane: a lane must render each segment once.
// A stale wake, sent by Begin to a lane that had already rendered the new
// segment and parked again, used to make that lane render it twice and let
// End return before another lane finished.
func TestForcedLanesRenderEachSegmentOnce(t *testing.T) {
	defer ForceEveryLane()()
	for _, c := range []struct{ tracks, lanes int }{{5, 4}, {12, 4}, {12, 8}, {3, 3}} {
		var next [12]atomic.Int64
		var broken atomic.Int64
		p := New(c.lanes, c.tracks, func(track, from, count int) {
			if !next[track].CompareAndSwap(int64(from), int64(from+count)) {
				broken.Add(1)
			}
		})
		for segment := range 3000 {
			start, count := (segment*7)%40, 32+(segment*13)%97
			for track := 0; track < c.tracks; track++ {
				next[track].Store(int64(start))
			}
			p.Begin(start, count)
			for frame := start; frame < start+count; frame++ {
				p.Ready(frame)
			}
			p.End()
			for track := 0; track < c.tracks; track++ {
				if got := next[track].Load(); got != int64(start+count) || broken.Load() != 0 {
					p.Close()
					t.Fatalf("tracks %d lanes %d segment %d: track %d rendered to %d of %d, %d runs out of order", c.tracks, c.lanes, segment, track, got, start+count, broken.Load())
				}
			}
		}
		p.Close()
	}
}
