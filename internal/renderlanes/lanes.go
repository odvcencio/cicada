// Package renderlanes spreads the per-track synthesis of an offline render
// over several goroutines, called lanes.
//
// A render that uses it splits the song into segments: runs of frames in which
// no event, automation point, scene change or other control reaches any track.
// Inside a segment every track's signal depends only on that track's own
// state, so the tracks can be synthesized in any order and on any goroutine.
// The caller renders each track's segment into a buffer of its own through a
// Pool, then mixes the buffers frame by frame in track order, exactly as the
// serial render mixes them. The assignment of tracks to lanes therefore
// decides only how long a segment takes, never what it contains.
//
// A Pool measures how long each track takes and, before every segment, gives
// each lane a share of the tracks with the longest first (the classic LPT
// rule), so one expensive voice does not share a lane with others. It runs a
// segment on fewer lanes, or on the caller alone, when the segment holds too
// little work to pay for waking a lane, and it never runs more lanes at once,
// across every Pool in the process, than the Go scheduler has processors.
package renderlanes

import (
	"math"
	"runtime"
	"slices"
	"sync/atomic"
	"time"
)

// MaxLanes bounds the lanes one render sets up.
const MaxLanes = 64

// MinLaneWork is the estimated work, in nanoseconds, that a lane must have in
// a segment before the Pool wakes it. Waking a parked lane and joining it
// costs a few microseconds (BenchmarkHandoff); a lane with less work than
// this would cost about as much as it saves.
const MinLaneWork = 40_000

// defaultCost is the estimate, in nanoseconds per frame, for a track the Pool
// has not measured yet.
const defaultCost = 100

// costWeight is the weight of the newest measurement in a track's running
// cost estimate.
const costWeight = 0.25

// rebalanceGain is the least improvement in estimated segment time for which
// the Pool moves tracks between lanes. Moving a track costs its cache.
const rebalanceGain = 0.95

// Jobs resolves a requested job count. Zero selects the usable processors:
// runtime.GOMAXPROCS, which follows the CPU affinity mask and cgroup CPU
// limits of the process. The result is 1 to MaxLanes.
func Jobs(requested int) int {
	if requested > 0 {
		return min(requested, MaxLanes)
	}
	return max(1, min(runtime.GOMAXPROCS(0), MaxLanes))
}

// Lanes is the number of lanes a render of tracks tracks sets up for jobs
// jobs: one per track at most, and 1 means the serial render.
func Lanes(jobs, tracks int) int {
	return max(1, min(jobs, tracks, MaxLanes))
}

// forceEveryLane makes worth return every lane; see ForceEveryLane.
var forceEveryLane atomic.Int32

// ForceEveryLane makes every Pool in the process run each segment on all of
// its lanes, whatever the segment's work, until the returned function is
// called. Tests use it to run the lanes on songs too light to use them; the
// process budget of busy lanes still applies.
func ForceEveryLane() (restore func()) {
	forceEveryLane.Add(1)
	return func() { forceEveryLane.Add(-1) }
}

// busy counts the worker lanes, across every Pool in the process, that are
// rendering a segment now. The goroutine that calls Run is not counted: it
// renders a lane itself.
var busy atomic.Int64

// acquire reserves up to want worker lanes from the process budget of
// limit and returns how many it got.
func acquire(want, limit int) int {
	for {
		current := busy.Load()
		free := int64(limit) - current
		if free <= 0 || want <= 0 {
			return 0
		}
		got := min(int64(want), free)
		if busy.CompareAndSwap(current, current+got) {
			return int(got)
		}
	}
}

func release(n int) {
	if n > 0 {
		busy.Add(-int64(n))
	}
}

// Pool renders the tracks of one render on up to Lanes goroutines. It is used
// by one goroutine, the render's; its own worker goroutines never call the
// owner except through the render function.
type Pool struct {
	lanes, tracks int
	// workerLimit is the most worker lanes that may run at once in the
	// process: GOMAXPROCS when the Pool was made, minus the caller's own.
	workerLimit int
	render      func(track, start, count int)

	cost     []float64 // estimated nanoseconds per frame, per track
	measured []bool
	took     []int64 // nanoseconds the last segment took, per track

	order  []int // tracks, grouped by lane
	bounds []int // lane l renders order[bounds[l]:bounds[l+1]]
	used   int   // lanes of the current assignment

	// Scratch for planning, kept to avoid allocation per segment.
	ranked      []int
	loads       []float64
	trialOrder  []int
	trialBounds []int
	trialLane   []int

	start, count int
	workers      []worker
	done         chan any // one value per finished worker lane: nil, or what it panicked with
	stop         chan struct{}
	closed       bool

	stats      Stats
	trackNanos []int64
}

type worker struct {
	wake   chan struct{}
	exited chan struct{}
}

// Stats describes how a Pool ran its segments.
type Stats struct {
	Segments         int64 // segments run
	ParallelSegments int64 // segments run on more than one lane
	MaxUsed          int   // the most lanes that ran one segment
	Frames           int64 // frames of all segments
	RunNanos         int64 // wall time spent in Run
	WorkNanos        int64 // time the tracks took, summed over every lane
	CriticalNanos    int64 // the busiest lane's time, summed over segments
	// TrackNanos is the time each track took, summed over segments.
	TrackNanos []int64
	// TrackCost is each track's estimated cost, in nanoseconds per frame.
	TrackCost []float64
}

// New starts a Pool of lanes lanes (the caller's own included) for tracks
// tracks. render synthesizes count frames of one track, starting at frame
// start of the caller's block, into storage owned by that track; the Pool
// calls it once per track per segment, from the caller's goroutine or from
// a worker. It returns nil when lanes is below 2: the render stays serial.
func New(lanes, tracks int, render func(track, start, count int)) *Pool {
	lanes = min(lanes, tracks, MaxLanes)
	if lanes < 2 || render == nil {
		return nil
	}
	p := &Pool{
		lanes: lanes, tracks: tracks, workerLimit: max(0, runtime.GOMAXPROCS(0)-1), render: render,
		cost: make([]float64, tracks), measured: make([]bool, tracks), took: make([]int64, tracks), trackNanos: make([]int64, tracks),
		order: make([]int, tracks), bounds: make([]int, lanes+1),
		ranked: make([]int, tracks), loads: make([]float64, lanes),
		trialOrder: make([]int, tracks), trialBounds: make([]int, lanes+1), trialLane: make([]int, tracks),
		workers: make([]worker, lanes-1), done: make(chan any, lanes), stop: make(chan struct{}),
	}
	for track := range p.order {
		p.order[track] = track
	}
	p.bounds[1] = tracks
	for lane := 2; lane <= lanes; lane++ {
		p.bounds[lane] = tracks
	}
	p.used = 1
	for i := range p.workers {
		p.workers[i] = worker{wake: make(chan struct{}, 1), exited: make(chan struct{})}
		go p.work(i + 1)
	}
	return p
}

// Lanes is the number of lanes the Pool set up, the caller's included.
func (p *Pool) Lanes() int {
	if p == nil {
		return 1
	}
	return p.lanes
}

// Stats reports how the Pool has run so far.
func (p *Pool) Stats() Stats {
	if p == nil {
		return Stats{}
	}
	stats := p.stats
	stats.TrackCost = slices.Clone(p.cost)
	stats.TrackNanos = slices.Clone(p.trackNanos)
	return stats
}

// Run renders count frames of every track, from frame start, and returns when
// all of them are done. A panic in any lane is raised again here, after every
// lane has stopped.
func (p *Pool) Run(start, count int) {
	began := time.Now()
	p.start, p.count = start, count
	want := p.worth(count)
	got := 0
	if want > 1 {
		got = acquire(want-1, p.workerLimit)
	}
	used := 1 + got
	p.assign(used)
	for lane := 1; lane < used; lane++ {
		p.workers[lane-1].wake <- struct{}{}
	}
	failure := p.safeLane(0)
	for lane := 1; lane < used; lane++ {
		if value := <-p.done; value != nil && failure == nil {
			failure = value
		}
	}
	release(got)
	if failure != nil {
		panic(failure)
	}
	p.observe(count)
	p.stats.Segments++
	if used > 1 {
		p.stats.ParallelSegments++
	}
	p.stats.MaxUsed = max(p.stats.MaxUsed, used)
	p.stats.Frames += int64(count)
	critical := int64(0)
	for lane := 0; lane < used; lane++ {
		sum := int64(0)
		for _, track := range p.order[p.bounds[lane]:p.bounds[lane+1]] {
			sum += p.took[track]
			p.trackNanos[track] += p.took[track]
			p.stats.WorkNanos += p.took[track]
		}
		critical = max(critical, sum)
	}
	p.stats.CriticalNanos += critical
	p.stats.RunNanos += time.Since(began).Nanoseconds()
}

// Close stops the worker lanes and waits for them to leave. It is safe to
// call more than once and on a nil Pool.
func (p *Pool) Close() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	close(p.stop)
	for i := range p.workers {
		<-p.workers[i].exited
	}
}

func (p *Pool) work(lane int) {
	w := &p.workers[lane-1]
	defer close(w.exited)
	for {
		select {
		case <-p.stop:
			return
		case <-w.wake:
			p.done <- p.safeLane(lane)
		}
	}
}

func (p *Pool) safeLane(lane int) (failure any) {
	defer func() {
		if value := recover(); value != nil {
			failure = value
		}
	}()
	tracks := p.order[p.bounds[lane]:p.bounds[lane+1]]
	before := time.Now()
	for _, track := range tracks {
		p.render(track, p.start, p.count)
		after := time.Now()
		p.took[track] = after.Sub(before).Nanoseconds()
		before = after
	}
	return nil
}

// estimate is a track's expected cost in nanoseconds per frame.
func (p *Pool) estimate(track int) float64 {
	if p.measured[track] {
		return p.cost[track]
	}
	return defaultCost
}

// worth is the number of lanes worth running for a segment of count frames:
// no more than the work fills at MinLaneWork per lane, and no more than it
// takes for the other tracks to fill the time of the heaviest one.
func (p *Pool) worth(count int) int {
	if forceEveryLane.Load() > 0 {
		return p.lanes
	}
	total, heaviest := 0.0, 0.0
	for track := 0; track < p.tracks; track++ {
		c := p.estimate(track)
		total += c
		heaviest = max(heaviest, c)
	}
	want := p.lanes
	if heaviest > 0 {
		// Five percent of slack keeps rounding from adding a lane that only
		// waits for the heaviest track.
		want = min(want, int(math.Ceil(total/heaviest-0.05)))
	}
	want = min(want, int(total*float64(count)/MinLaneWork))
	return max(1, want)
}

// assign spreads the tracks over used lanes, heaviest first, each to the lane
// with the least estimated work so far. It keeps the current assignment
// unless the lane count changed or the new one is clearly faster.
func (p *Pool) assign(used int) {
	if used == 1 {
		if p.used != 1 {
			for track := range p.order {
				p.order[track] = track
			}
			p.bounds[0], p.bounds[1] = 0, p.tracks
			for lane := 2; lane <= p.lanes; lane++ {
				p.bounds[lane] = p.tracks
			}
			p.used = 1
		}
		return
	}
	for track := range p.ranked {
		p.ranked[track] = track
	}
	slices.SortStableFunc(p.ranked, func(a, b int) int {
		ca, cb := p.estimate(a), p.estimate(b)
		switch {
		case ca > cb:
			return -1
		case ca < cb:
			return 1
		}
		return 0
	})
	loads := p.loads[:used]
	clear(loads)
	for _, track := range p.ranked {
		lightest := 0
		for lane := 1; lane < used; lane++ {
			if loads[lane] < loads[lightest] {
				lightest = lane
			}
		}
		loads[lightest] += p.estimate(track)
		p.trialLane[track] = lightest
	}
	if p.used == used {
		trial := slices.Max(loads)
		current := p.makespan()
		if trial >= current*rebalanceGain {
			return
		}
	}
	// Lay the lanes out in order; each lane keeps its tracks in track order.
	n := 0
	for lane := 0; lane < used; lane++ {
		p.trialBounds[lane] = n
		for track := 0; track < p.tracks; track++ {
			if p.trialLane[track] == lane {
				p.trialOrder[n] = track
				n++
			}
		}
	}
	for lane := used; lane <= p.lanes; lane++ {
		p.trialBounds[lane] = n
	}
	p.order, p.trialOrder = p.trialOrder, p.order
	p.bounds, p.trialBounds = p.trialBounds, p.bounds
	p.used = used
}

// makespan is the estimated time of the current assignment: its busiest lane.
func (p *Pool) makespan() float64 {
	longest := 0.0
	for lane := 0; lane < p.used; lane++ {
		sum := 0.0
		for _, track := range p.order[p.bounds[lane]:p.bounds[lane+1]] {
			sum += p.estimate(track)
		}
		longest = max(longest, sum)
	}
	return longest
}

// observe folds the segment's measured times into the cost estimates.
func (p *Pool) observe(count int) {
	if count <= 0 {
		return
	}
	for track := 0; track < p.tracks; track++ {
		perFrame := float64(p.took[track]) / float64(count)
		if !p.measured[track] {
			p.cost[track], p.measured[track] = perFrame, true
			continue
		}
		p.cost[track] += costWeight * (perFrame - p.cost[track])
	}
}
