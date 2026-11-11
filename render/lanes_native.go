//go:build !tinygo && !js

package render

import (
	"math"
	"sync/atomic"

	"m31labs.dev/cicada/internal/renderlanes"
)

// minLaneSegment is the shortest segment the direct renderer hands to its
// lanes; a shorter one renders serially, as the engine's parallel mode does.
const minLaneSegment = 32

// trackLanes synthesizes the direct renderer's tracks on several goroutines.
// renderBlock asks it for one segment at a time: frames in which no event or
// automation point reaches any track. Each lane renders its tracks of the
// segment into the tracks' own spans of left and right and publishes them in
// chunks; renderBlock mixes each frame, in track order, exactly as the serial
// loop does, as soon as every lane has published it.
type trackLanes struct {
	pool        *renderlanes.Pool
	tracks      []trackRuntime
	left, right []float32 // per track, count frames from start
	errs        []error   // the first fault of each track in the segment
	// errAt is, per track, the block frame of that fault or math.MaxInt64;
	// a lane stores it after the error.
	errAt      []atomic.Int64
	start, end int // the segment, in frames of the current block
	count      int
	prepared   bool
	readyUntil int // frames before this are published by every lane
}

func resolveJobs(requested int) int { return renderlanes.Jobs(requested) }

// plannedLanes is the number of lanes the direct renderer sets up.
func plannedLanes(jobs, tracks int) int { return renderlanes.Lanes(jobs, tracks) }

// newTrackLanes prepares lanes for tracks, or returns nil when the render
// stays serial. block is the most frames one renderBlock call renders.
func newTrackLanes(tracks []trackRuntime, jobs, block int) *trackLanes {
	if plannedLanes(jobs, len(tracks)) < 2 || block < 1 {
		return nil
	}
	l := &trackLanes{
		tracks: tracks,
		left:   make([]float32, len(tracks)*block), right: make([]float32, len(tracks)*block),
		errs: make([]error, len(tracks)), errAt: make([]atomic.Int64, len(tracks)),
	}
	l.pool = renderlanes.New(jobs, len(tracks), l.renderTrack)
	if l.pool == nil {
		return nil
	}
	return l
}

func (l *trackLanes) lanes() int {
	if l == nil {
		return 1
	}
	return l.pool.Lanes()
}

func (l *trackLanes) close() {
	if l != nil {
		l.pool.Close()
	}
}

// begin starts a block: segments never cross renderBlock calls.
func (l *trackLanes) begin() { l.end, l.prepared = 0, false }

// finish ends the current segment: every lane is done with its tracks.
func (l *trackLanes) finish() {
	l.pool.End()
	l.prepared = false
}

// prepare starts the lanes on frames start to end of the current block, after
// the previous segment has ended. A short segment is left to the serial loop.
func (l *trackLanes) prepare(tracks []trackRuntime, start, end int) {
	l.finish()
	l.start, l.end = start, max(end, start+1)
	l.count = l.end - l.start
	l.prepared = l.count >= minLaneSegment && len(tracks) == len(l.errs) && len(tracks)*l.count <= len(l.left)
	if !l.prepared {
		return
	}
	l.tracks, l.readyUntil = tracks, start
	l.pool.Begin(l.start, l.count)
}

// renderTrack runs on a lane, with consecutive runs of frames that cover the
// segment: the track belongs to the lane until the segment ends.
func (l *trackLanes) renderTrack(track, from, count int) {
	if from == l.start {
		l.errs[track] = nil
		l.errAt[track].Store(math.MaxInt64)
	} else if l.errAt[track].Load() != math.MaxInt64 {
		return
	}
	t := &l.tracks[track]
	base := track*l.count + from - l.start
	for i := 0; i < count; i++ {
		left, right, err := t.nextRaw()
		if err != nil {
			l.errs[track] = err
			l.errAt[track].Store(int64(from + i))
			return
		}
		l.left[base+i], l.right[base+i] = left, right
	}
}

// sample is the track's frame of the prepared segment, or the fault the
// serial loop would have returned at that frame.
func (l *trackLanes) sample(track, frame int) (float32, float32, error) {
	if frame >= l.readyUntil {
		l.readyUntil = l.pool.Ready(frame)
	}
	if int64(frame) >= l.errAt[track].Load() {
		return 0, 0, l.errs[track]
	}
	i := track*l.count + frame - l.start
	return l.left[i], l.right[i], nil
}
