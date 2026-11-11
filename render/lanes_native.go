//go:build !tinygo && !js

package render

import "m31labs.dev/cicada/internal/renderlanes"

// minLaneSegment is the shortest segment the direct renderer hands to its
// lanes; a shorter one renders serially, as the engine's parallel mode does.
const minLaneSegment = 32

// trackLanes synthesizes the direct renderer's tracks on several goroutines.
// renderBlock asks it for one segment at a time: frames in which no event or
// automation point reaches any track. Each lane renders whole tracks of the
// segment into the track's own span of left and right; renderBlock then mixes
// them in track order, frame by frame, exactly as the serial loop does.
type trackLanes struct {
	pool        *renderlanes.Pool
	tracks      []trackRuntime
	left, right []float32 // per track, count frames from start
	errs        []error   // the first fault of each track in the segment
	errAt       []int     // the block frame of that fault
	start, end  int       // the segment, in frames of the current block
	count       int
	prepared    bool
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
		errs: make([]error, len(tracks)), errAt: make([]int, len(tracks)),
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

func (l *trackLanes) finish() { l.prepared = false }

// prepare renders frames start to end of the current block for every track.
// A short segment is left to the serial loop.
func (l *trackLanes) prepare(tracks []trackRuntime, start, end int) {
	l.start, l.end = start, max(end, start+1)
	l.count = l.end - l.start
	l.prepared = l.count >= minLaneSegment && len(tracks) == len(l.errs) && len(tracks)*l.count <= len(l.left)
	if !l.prepared {
		return
	}
	l.tracks = tracks
	l.pool.Run(l.start, l.count)
}

// renderTrack runs on a lane: the track belongs to it until the segment ends.
func (l *trackLanes) renderTrack(track, start, count int) {
	t := &l.tracks[track]
	base := track * count
	l.errs[track] = nil
	for i := 0; i < count; i++ {
		left, right, err := t.nextRaw()
		if err != nil {
			l.errs[track], l.errAt[track] = err, start+i
			return
		}
		l.left[base+i], l.right[base+i] = left, right
	}
}

// sample is the track's frame of the prepared segment, or the fault the
// serial loop would have returned at that frame.
func (l *trackLanes) sample(track, frame int) (float32, float32, error) {
	if err := l.errs[track]; err != nil && frame >= l.errAt[track] {
		return 0, 0, err
	}
	i := track*l.count + frame - l.start
	return l.left[i], l.right[i], nil
}
