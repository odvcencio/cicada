// Package renderlanes spreads the per-track synthesis of an offline render
// over several goroutines, called lanes.
//
// A render that uses it splits the song into segments: runs of frames in which
// no event, automation point, scene change or other control reaches any track.
// Inside a segment every track's signal depends only on that track's own
// state, so the tracks can be synthesized in any order and on any goroutine.
// Each lane renders its tracks' frames into buffers of their own, and the
// caller mixes the buffers frame by frame in track order, exactly as the
// serial render mixes them. The assignment of tracks to lanes therefore
// decides only how long a segment takes, never what it contains.
//
// Lanes render a segment in chunks of chunkFrames frames and publish each
// chunk as they finish it, so the caller mixes a chunk while the lanes render
// the next one: the serial mix overlaps the synthesis instead of following it.
// The caller is a lane too: it renders its own tracks just before it mixes
// them, and works ahead on them while it waits for the others.
//
// A Pool measures how long each track and the caller's mix take and, before
// every segment, gives each lane a share of the tracks with the longest first
// (the classic LPT rule), counting the mix as work already on the caller's
// lane, so one expensive voice does not share a lane with others. It runs a
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

// chunkFrames is the number of frames of each of its tracks that a lane
// renders before it publishes them to the caller. The caller mixes the last
// chunk of a segment after the slowest lane finishes it, so a short chunk
// keeps that tail short.
const chunkFrames = 64

// spinWait is how long a worker lane that finished a segment watches for
// the next one before it parks. Segments of a busy song follow each other
// within a few hundred microseconds, and waking a parked goroutine on a loaded
// host can take as long.
const spinWait = 200 * time.Microsecond

// defaultCost is the estimate, in nanoseconds per frame, for a track the Pool
// has not measured yet.
const defaultCost = 100

// costWeight is the weight of the newest measurement in a running cost
// estimate.
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
// rendering a segment now. The goroutine that calls Begin is not counted: it
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

// Pool renders the tracks of one render on up to Lanes goroutines. One
// goroutine, the render's, calls its methods; its worker goroutines call only
// the render function.
type Pool struct {
	lanes, tracks int
	// workerLimit is the most worker lanes that may run at once in the
	// process: GOMAXPROCS when the Pool was made, minus the caller's own.
	workerLimit int
	render      func(track, from, count int)

	cost     []float64 // estimated nanoseconds per frame, per track
	measured []bool
	took     []int64 // nanoseconds each track took in the current segment
	// mixCost is the estimated nanoseconds per frame the caller spends
	// outside the Pool during a segment: the mix and the control work.
	mixCost     float64
	mixMeasured bool

	order  []int // tracks, grouped by lane
	bounds []int // lane l renders order[bounds[l]:bounds[l+1]]
	used   int   // lanes of the current assignment

	// Scratch for planning, kept to avoid allocation per segment.
	ranked      []int
	loads       []float64
	trialOrder  []int
	trialBounds []int
	trialLane   []int

	// The current segment. Begin writes these before it wakes the workers,
	// which only read them.
	start, count, chunks int
	active               bool
	got                  int       // worker lanes reserved from the process budget
	own                  int       // chunks of lane 0 rendered so far
	readyUntil           int       // frames before this are published by every lane
	began                time.Time // when Begin returned
	begun                time.Time // when Begin was called
	waited               int64     // nanoseconds the caller waited in Ready
	inside               int64     // nanoseconds the caller spent in Ready
	progress             []progress
	failed               atomic.Bool
	// generation counts segments; usedLanes is the current segment's lane
	// count. Begin stores both after the segment's fields, so a worker that
	// loads a new generation reads them too.
	generation atomic.Int64
	usedLanes  atomic.Int32
	stopping   atomic.Bool

	workers []worker
	done    chan any // one value per finished worker lane: nil, or what it panicked with
	stop    chan struct{}
	closed  bool

	stats      Stats
	trackNanos []int64
}

// progress counts the chunks one lane has published in the current segment.
type progress struct {
	chunks atomic.Int64
	// Keep neighbouring lanes' counters off one cache line.
	_ [7]int64
}

type worker struct {
	wake   chan struct{}
	exited chan struct{}
	// parked is set while the worker waits on wake; Begin sends a wake only
	// to a worker whose parked flag it clears.
	parked atomic.Bool
	_      [7]int64
}

// Stats describes how a Pool ran its segments.
type Stats struct {
	Segments         int64 // segments run
	ParallelSegments int64 // segments run on more than one lane
	MaxUsed          int   // the most lanes that ran one segment
	Frames           int64 // frames of all segments
	SegmentNanos     int64 // wall time from Begin to the end of End
	WaitNanos        int64 // time the caller waited in Ready for other lanes
	// CriticalNanos sums, over segments, the busiest lane's work: its
	// tracks' time, and on the caller's lane the mix too.
	CriticalNanos int64
	WorkNanos     int64 // time the tracks took, summed over every lane
	MixNanos      int64 // time the caller spent outside the Pool in segments
	// TrackNanos is the time each track took, summed over segments, and
	// TrackCost each track's estimated cost in nanoseconds per frame.
	TrackNanos []int64
	TrackCost  []float64
}

// New starts a Pool of lanes lanes (the caller's own included) for tracks
// tracks. render synthesizes count frames of one track, from frame from of the
// caller's block, into storage owned by that track; within a segment the Pool
// calls it for each track with consecutive runs of frames that cover the
// segment, from the caller's goroutine or from one worker. It returns nil
// when lanes is below 2: the render stays serial.
func New(lanes, tracks int, render func(track, from, count int)) *Pool {
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
		progress: make([]progress, lanes),
		workers:  make([]worker, lanes-1), done: make(chan any, lanes), stop: make(chan struct{}),
	}
	for track := range p.order {
		p.order[track] = track
	}
	for lane := 1; lane <= lanes; lane++ {
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

// Begin starts a segment of count frames from frame start of the caller's
// block. Before it reads a frame of the segment the caller calls Ready, and
// after the segment, or when it stops early, End. A segment still running
// is ended first.
func (p *Pool) Begin(start, count int) {
	if p.active {
		p.End()
	}
	p.begun = time.Now()
	p.start, p.count = start, count
	p.chunks = (count + chunkFrames - 1) / chunkFrames
	want := p.worth(count)
	p.got = 0
	if want > 1 {
		p.got = acquire(want-1, p.workerLimit)
	}
	used := 1 + p.got
	p.assign(used)
	clear(p.took)
	p.failed.Store(false)
	for lane := 1; lane < used; lane++ {
		p.progress[lane].chunks.Store(0)
	}
	p.own, p.inside, p.waited, p.active = 0, 0, 0, true
	p.usedLanes.Store(int32(used))
	p.generation.Add(1)
	for lane := 1; lane < used; lane++ {
		if w := &p.workers[lane-1]; w.parked.CompareAndSwap(true, false) {
			w.wake <- struct{}{}
		}
	}
	p.readyUntil = start
	if used == 1 {
		// Nothing to overlap: render every track now, on the caller.
		for p.own < p.chunks {
			p.renderOwnChunk()
		}
		p.readyUntil = start + count
	}
	p.began = time.Now()
}

// Ready makes every track's samples readable up to and including frame, a
// frame of the current segment, and returns the first frame after it that
// may not be: the caller need not call Ready again before that frame. The
// caller reads frames in order. A panic in a lane is raised here, after every
// lane has stopped.
func (p *Pool) Ready(frame int) int {
	if frame < p.readyUntil {
		return p.readyUntil
	}
	entered := time.Now()
	need := min((frame-p.start)/chunkFrames+1, p.chunks)
	var waitFrom time.Time
	spins := 0
	for {
		if p.own < need {
			p.renderOwnChunk()
			continue
		}
		reached := p.reached()
		if reached >= need {
			need = min(reached, p.own)
			break
		}
		if p.own < p.chunks {
			// Work ahead on the caller's own tracks while the others finish.
			p.renderOwnChunk()
			continue
		}
		if spins == 0 {
			waitFrom = time.Now()
		}
		if spins++; spins < 1000 {
			runtime.Gosched()
		} else {
			time.Sleep(10 * time.Microsecond)
		}
	}
	if spins > 0 {
		p.waited += time.Since(waitFrom).Nanoseconds()
	}
	if p.failed.Load() {
		p.End()
	}
	p.readyUntil = min(p.start+need*chunkFrames, p.start+p.count)
	p.inside += time.Since(entered).Nanoseconds()
	return p.readyUntil
}

// reached is the number of chunks every worker lane has published.
func (p *Pool) reached() int {
	reached := p.chunks
	for lane := 1; lane < p.used; lane++ {
		reached = min(reached, int(p.progress[lane].chunks.Load()))
	}
	return reached
}

// End waits until every lane has finished the segment and returns the
// workers' share of the process budget. A panic in a lane is raised here.
// End without a segment, or on a nil Pool, does nothing.
func (p *Pool) End() {
	if p == nil || !p.active {
		return
	}
	mix := time.Since(p.began).Nanoseconds() - p.inside
	complete := p.own == p.chunks
	var failure any
	for lane := 1; lane < p.used; lane++ {
		if value := <-p.done; value != nil && failure == nil {
			failure = value
		}
	}
	release(p.got)
	p.got, p.active = 0, false
	if failure != nil {
		panic(failure)
	}
	if !complete {
		// The caller stopped early, for example at a fault: the measurement
		// covers part of the segment only.
		return
	}
	p.observe(p.count, mix)
	p.stats.Segments++
	if p.used > 1 {
		p.stats.ParallelSegments++
	}
	p.stats.MaxUsed = max(p.stats.MaxUsed, p.used)
	p.stats.Frames += int64(p.count)
	p.stats.SegmentNanos += time.Since(p.begun).Nanoseconds()
	p.stats.WaitNanos += p.waited
	p.stats.MixNanos += max(0, mix)
	for track, took := range p.took {
		p.trackNanos[track] += took
		p.stats.WorkNanos += took
	}
	critical := int64(0)
	for lane := 0; lane < p.used; lane++ {
		sum := int64(0)
		if lane == 0 {
			sum = max(0, mix)
		}
		for _, track := range p.order[p.bounds[lane]:p.bounds[lane+1]] {
			sum += p.took[track]
		}
		critical = max(critical, sum)
	}
	p.stats.CriticalNanos += critical
}

// Run renders a whole segment before it returns: Begin, Ready for its last
// frame, and End.
func (p *Pool) Run(start, count int) {
	p.Begin(start, count)
	p.Ready(start + count - 1)
	p.End()
}

// Close stops the worker lanes and waits for them to leave, after any
// segment they are rendering. It is safe to call more than once and on a nil
// Pool.
func (p *Pool) Close() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	if p.active {
		// Workers finish the segment and then see stop; their results are
		// no longer read.
		release(p.got)
		p.got, p.active = 0, false
	}
	p.stopping.Store(true)
	close(p.stop)
	for i := range p.workers {
		<-p.workers[i].exited
	}
}

func (p *Pool) work(lane int) {
	w := &p.workers[lane-1]
	defer close(w.exited)
	var seen int64
	for {
		generation, ok := p.nextSegment(w, seen)
		if !ok {
			return
		}
		seen = generation
		if lane < int(p.usedLanes.Load()) {
			p.done <- p.renderLane(lane)
		}
	}
}

// nextSegment waits for a segment newer than seen: it watches for spinWait,
// then parks until Begin wakes it. It reports false when the Pool closes.
func (p *Pool) nextSegment(w *worker, seen int64) (int64, bool) {
	deadline := time.Now().Add(spinWait)
	for i := 1; ; i++ {
		if generation := p.generation.Load(); generation != seen {
			return generation, true
		}
		if p.stopping.Load() {
			return 0, false
		}
		if i%64 == 0 && !time.Now().Before(deadline) {
			break
		}
		runtime.Gosched()
	}
	w.parked.Store(true)
	if generation := p.generation.Load(); generation != seen {
		if !w.parked.CompareAndSwap(true, false) {
			<-w.wake // Begin saw the flag and sent a wake: take it.
		}
		return generation, true
	}
	select {
	case <-w.wake:
		return p.generation.Load(), true
	case <-p.stop:
		return 0, false
	}
}

// renderLane renders a worker lane's tracks for the segment, chunk by chunk,
// publishing each chunk when all of its tracks have it.
func (p *Pool) renderLane(lane int) (failure any) {
	defer func() {
		if value := recover(); value != nil {
			failure = value
			// Publish the failure before the progress the caller waits for.
			p.failed.Store(true)
			p.progress[lane].chunks.Store(int64(p.chunks))
		}
	}()
	tracks := p.order[p.bounds[lane]:p.bounds[lane+1]]
	for chunk := range p.chunks {
		p.renderChunk(tracks, chunk)
		p.progress[lane].chunks.Store(int64(chunk + 1))
	}
	return nil
}

// renderOwnChunk renders the next chunk of the caller's lane. A panic in it
// is raised after every worker lane has stopped.
func (p *Pool) renderOwnChunk() {
	defer func() {
		if value := recover(); value != nil {
			p.own = p.chunks
			for lane := 1; lane < p.used; lane++ {
				<-p.done
			}
			release(p.got)
			p.got, p.active = 0, false
			panic(value)
		}
	}()
	p.renderChunk(p.order[p.bounds[0]:p.bounds[1]], p.own)
	p.own++
}

func (p *Pool) renderChunk(tracks []int, chunk int) {
	from := p.start + chunk*chunkFrames
	count := min(chunkFrames, p.start+p.count-from)
	before := time.Now()
	for _, track := range tracks {
		p.render(track, from, count)
		after := time.Now()
		p.took[track] += after.Sub(before).Nanoseconds()
		before = after
	}
}

// estimate is a track's expected cost in nanoseconds per frame.
func (p *Pool) estimate(track int) float64 {
	if p.measured[track] {
		return p.cost[track]
	}
	return defaultCost
}

// worth is the number of lanes worth running for a segment of count frames:
// no more than the work, the caller's mix included, fills at MinLaneWork per
// lane, and no more than it takes for the rest of the work to fill the time
// of the heaviest track or of the mix.
func (p *Pool) worth(count int) int {
	if forceEveryLane.Load() > 0 {
		return p.lanes
	}
	total, heaviest := p.mixCost, p.mixCost
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
// with the least estimated work so far; the caller's lane starts with its mix.
// It keeps the current assignment unless the lane count changed or the new
// one is clearly faster.
func (p *Pool) assign(used int) {
	if used == 1 {
		if p.used != 1 {
			for track := range p.order {
				p.order[track] = track
			}
			p.bounds[0] = 0
			for lane := 1; lane <= p.lanes; lane++ {
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
	loads[0] = p.mixCost
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

// makespan is the estimated time of the current assignment: its busiest
// lane, the caller's mix included.
func (p *Pool) makespan() float64 {
	longest := 0.0
	for lane := 0; lane < p.used; lane++ {
		sum := 0.0
		if lane == 0 {
			sum = p.mixCost
		}
		for _, track := range p.order[p.bounds[lane]:p.bounds[lane+1]] {
			sum += p.estimate(track)
		}
		longest = max(longest, sum)
	}
	return longest
}

// observe folds the segment's measured times into the cost estimates.
func (p *Pool) observe(count int, mix int64) {
	if count <= 0 {
		return
	}
	update := func(cost *float64, measured *bool, perFrame float64) {
		if !*measured {
			*cost, *measured = perFrame, true
			return
		}
		*cost += costWeight * (perFrame - *cost)
	}
	for track := 0; track < p.tracks; track++ {
		update(&p.cost[track], &p.measured[track], float64(p.took[track])/float64(count))
	}
	update(&p.mixCost, &p.mixMeasured, float64(max(0, mix))/float64(count))
}
