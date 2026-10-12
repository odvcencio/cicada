//go:build !tinygo && !js

package engine

import (
	"runtime"
	"sync/atomic"
	"time"
)

const maxParallelLanes = 4

// parallelState owns one prepared stereo buffer per track. Only the callback
// assigns jobs; workers touch disjoint track sources/inserts and buffer spans.
// Control, transport, sends, buses, reduction and meters remain on the caller.
type parallelState struct {
	engine       *Engine
	lanes        int
	left, right  []float32
	faultFrame   []int
	faultCode    []uint16
	workers      [maxParallelLanes - 1]parallelWorker
	stop         chan struct{}
	retired      atomic.Bool
	generation   uint64
	start, count int
	prepared     bool
	priority     string
	// A finite native scheduling allowance catches an abandoned worker. This
	// is a safety bound, not a promise that a late callback meets its deadline.
	waitFloor time.Duration
}

type parallelWorker struct {
	pool       *parallelState
	lane       int
	wake       chan struct{}
	ready      chan struct{}
	joined     chan struct{}
	start      int
	count      int
	generation uint64
	completed  atomic.Uint64
	failed     atomic.Bool
	priority   string
	// Keep completion writes from neighboring workers off the same cache line.
	padding [8]uint64
}

// EnableParallel prepares native DSP workers outside the render callback.
// workers counts the caller's lane too: 4 creates three background workers.
// Zero selects up to four lanes for sessions of at least 32 tracks. Explicit
// two-to-four-lane requests allow eight-track sessions for testing/tuning.
// One-core hosts retain the serial path. Call before playback; configuration
// is immutable and all worker startup finishes here.
func (e *Engine) EnableParallel(workers int) error {
	if workers < 0 || workers > maxParallelLanes {
		return Error("parallel DSP requires zero to four render lanes")
	}
	if e.parallel != nil {
		return Error("parallel DSP is already configured")
	}
	if workers == 0 {
		if e.tracks < 32 {
			return nil
		}
		workers = maxParallelLanes
	}
	workers = min(workers, runtime.GOMAXPROCS(0), e.tracks)
	if workers < 2 || e.tracks < 8 {
		return nil
	}
	p := &parallelState{
		engine: e, lanes: workers,
		left: make([]float32, e.tracks*e.maxBlock), right: make([]float32, e.tracks*e.maxBlock),
		faultFrame: make([]int, e.tracks), faultCode: make([]uint16, e.tracks),
		stop: make(chan struct{}), waitFloor: 100 * time.Millisecond,
	}
	for lane := 1; lane < workers; lane++ {
		w := &p.workers[lane-1]
		w.pool, w.lane = p, lane
		w.wake, w.ready, w.joined = make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	}
	e.parallel = p
	for lane := 1; lane < workers; lane++ {
		go p.workers[lane-1].run()
	}
	// Finish worker/thread startup on the host before the first callback.
	for lane := 1; lane < workers; lane++ {
		<-p.workers[lane-1].ready
	}
	p.priority = p.workers[0].priority
	for lane := 2; lane < workers; lane++ {
		if p.workers[lane-1].priority != p.priority {
			p.priority = "mixed worker priority; normal OS scheduling fallback"
			break
		}
	}
	return nil
}

// Retired pools remain available until host teardown: a timeout must never
// cause serial fallback to mutate voices that an outstanding worker owns.
func (e *Engine) parallelAvailable() bool { return e.parallel != nil }

// ParallelWorkers includes the callback lane. These immutable diagnostics are
// safe for host status polling; they do not promise hardware round-trip latency.
func (e *Engine) ParallelWorkers() int {
	if e.parallel == nil {
		return 1
	}
	return e.parallel.lanes
}
func (e *Engine) ParallelPriority() string {
	if e.parallel == nil {
		return "serial"
	}
	return e.parallel.priority
}

func (e *Engine) parallelRetired() bool {
	return e.parallel != nil && e.parallel.retired.Load()
}

// parallelQuiescent is callback-safe and conservative. It only reports track
// ownership returned after completion publication or final worker exit.
func (e *Engine) parallelQuiescent() bool {
	p := e.parallel
	if p == nil {
		return true
	}
	for lane := 1; lane < p.lanes; lane++ {
		w := &p.workers[lane-1]
		if w.completed.Load() == p.generation {
			continue
		}
		select {
		case <-w.joined:
		default:
			return false
		}
	}
	return true
}

func (w *parallelWorker) run() {
	runtime.LockOSThread()
	leave := leaveParallelPriority
	defer func() {
		// Never reuse a thread whose MMCSS registration could not be
		// restored. A goroutine exiting while locked discards its OS thread.
		if leave() {
			runtime.UnlockOSThread()
		}
		close(w.joined)
	}()
	defer func() {
		if recover() != nil {
			w.failed.Store(true)
		}
	}()
	cleanup, priority := enterParallelPriority()
	leave = cleanup
	w.priority = priority
	close(w.ready)
	for {
		select {
		case <-w.pool.stop:
			return
		case <-w.wake:
			if w.pool.retired.Load() {
				return
			}
			// Callback writes happen before wake receive; completed publishes
			// all signal state/buffer writes before the callback reads them.
			generation := w.generation
			w.pool.renderLane(w.lane, w.start, w.count)
			w.completed.Store(generation)
		}
	}
}

func (p *parallelState) renderLane(lane, start, count int) {
	end := start + count
	for track := lane; track < p.engine.tracks; track += p.lanes {
		p.faultFrame[track], p.faultCode[track] = end, 0
		// Pack the current segment densely inside the preallocated arena.
		// A 128-frame callback need not stride over maxBlock=4096 padding for
		// every track during deterministic sample-by-sample reduction.
		base := track * count
		for frame := start; frame < end; frame++ {
			if frame&31 == 0 && p.retired.Load() {
				return
			}
			left, right, fault := p.engine.nextRawTrack(track)
			p.left[base+frame-start], p.right[base+frame-start] = left, right
			if fault != 0 {
				p.faultFrame[track], p.faultCode[track] = frame, fault
				break
			}
		}
	}
}

// parallelPrepareSegment renders raw tracks over a control-stable interval.
// Its signal reduction is intentionally left to the callback in track order.
// A returned fault frame is absolute within the current output block.
func (e *Engine) parallelPrepareSegment(start, count int) (faultFrame int, fault uint16) {
	p := e.parallel
	if p == nil {
		return -1, 0
	}
	p.prepared = false
	if p.retired.Load() {
		return start, FaultParallelWorker
	}
	if start < 0 || count < 1 || start > e.maxBlock-count {
		return start, FaultRenderBlock
	}
	// Tiny event intervals do not amortize worker wakeups. Rendering remains
	// sample-accurate through the serial nextRawTrack fallback.
	if count < 32 {
		return -1, 0
	}
	p.generation++
	if p.generation == 0 {
		e.retireParallel()
		return start, FaultParallelWorker
	}
	// A block-scaled bound also permits large offline blocks. There are no
	// callback-created timers, waits on channels, locks or new goroutines.
	budget := max(p.waitFloor, time.Duration(count)*time.Second*8/time.Duration(e.sampleRate))
	deadline := time.Now().Add(budget)
	for lane := 1; lane < p.lanes; lane++ {
		w := &p.workers[lane-1]
		w.start, w.count, w.generation = start, count, p.generation
		select {
		case w.wake <- struct{}{}:
		default:
			e.retireParallel()
			return start, FaultParallelWorker
		}
	}
	p.renderLane(0, start, count)
	for {
		complete := true
		for lane := 1; lane < p.lanes; lane++ {
			w := &p.workers[lane-1]
			if w.failed.Load() {
				e.retireParallel()
				return start, FaultParallelWorker
			}
			if w.completed.Load() != p.generation {
				complete = false
			}
		}
		if complete {
			break
		}
		if p.retired.Load() || !time.Now().Before(deadline) {
			e.retireParallel()
			return start, FaultParallelWorker
		}
		// Workers block on wake while idle. During this finite active join,
		// yield the caller's P so even constrained hosts can schedule them.
		runtime.Gosched()
	}
	if p.retired.Load() {
		return start, FaultParallelWorker
	}
	p.start, p.count, p.prepared = start, count, true
	faultFrame = start + count
	for track := 0; track < e.tracks; track++ {
		if p.faultCode[track] != 0 && p.faultFrame[track] < faultFrame {
			faultFrame, fault = p.faultFrame[track], p.faultCode[track]
		}
	}
	if fault == 0 {
		return -1, 0
	}
	return faultFrame, fault
}

func (e *Engine) renderedTrack(track, frame int) (left, right float32, fault uint16) {
	p := e.parallel
	if p != nil {
		if p.retired.Load() {
			return 0, 0, FaultParallelWorker
		}
		if p.prepared {
			if track < 0 || track >= e.tracks || frame < p.start || frame >= p.start+p.count {
				return 0, 0, FaultRenderBlock
			}
			if p.faultCode[track] != 0 && frame >= p.faultFrame[track] {
				return 0, 0, p.faultCode[track]
			}
			index := track*p.count + frame - p.start
			return p.left[index], p.right[index], 0
		}
	}
	return e.nextRawTrack(track)
}

func (e *Engine) parallelEndSegment() {
	if e.parallel != nil {
		e.parallel.prepared = false
	}
}

// retireParallel never joins or mutates track/clip state. Workers see the
// cancellation flag between short runs and leave their idle channel waits.
func (e *Engine) retireParallel() {
	if p := e.parallel; p != nil && !p.retired.Swap(true) {
		close(p.stop)
	}
}

// closeParallel is host-only, after callbacks stop. Join BEFORE resetting
// voices or stopping clip readers: timed-out jobs may still own those states.
func (e *Engine) closeParallel() {
	e.retireParallel()
	if p := e.parallel; p != nil {
		for lane := 1; lane < p.lanes; lane++ {
			<-p.workers[lane-1].joined
		}
		p.prepared = false
	}
}
