package render

// AutoJobs, as Options.Jobs, asks for the automatic job count: the smaller of
// 4 and half the usable CPUs, at least 1.
const AutoJobs = -1

// The reasons a render stayed serial (LaneReport.SerialReason). The list is
// SerialReasons; docs/manual/agents.md documents each one.
const (
	// SerialJobs: the job count is 1, because no lanes were asked for
	// (Options.Jobs zero, the default) or the automatic count chose 1.
	SerialJobs = "jobs"
	// SerialOneTrack: the song has one track; lanes split tracks.
	SerialOneTrack = "one_track"
	// SerialGraphLFO: a graph LFO shares the scheduled engine's serial frame
	// clock.
	SerialGraphLFO = "graph_lfo"
	// SerialReplay: the render replayed a stored mix and synthesized nothing.
	SerialReplay = "replay"
	// SerialNoProcessor: lanes were set up, but no processor was free for
	// them (GOMAXPROCS 1, or other renders in the process held them all).
	SerialNoProcessor = "no_processor"
	// SerialLight: every segment had too little work to pay for waking a lane.
	SerialLight = "light"
)

// SerialReasons lists every LaneReport.SerialReason code.
var SerialReasons = []string{SerialJobs, SerialOneTrack, SerialGraphLFO, SerialReplay, SerialNoProcessor, SerialLight}

// LaneReport says how a render used its lanes (Options.Jobs), so a caller can
// tell why a render was or was not faster. The counts other than
// JobsRequested, Jobs and Lanes depend on scheduling and load and may differ
// between runs that write the same bytes.
type LaneReport struct {
	// JobsRequested is Options.Jobs as given: 0 (serial), AutoJobs or a count.
	JobsRequested int
	// LanesRequested is the lane count asked for before the CPU and track
	// caps: the count given, the automatic choice, or 1 for serial.
	LanesRequested int
	// Jobs is the most lanes allowed: LanesRequested capped at the CPUs.
	Jobs int
	// Lanes is the number of lanes set up: Jobs capped at the song's tracks,
	// or 1 when the render was serial.
	Lanes int
	// LanesPeak is the most lanes that rendered one segment at once.
	LanesPeak int
	// Segments counts the runs of frames between control changes that the
	// lanes were offered; ParallelSegments, those that ran on two or more.
	Segments, ParallelSegments int64
	// SerialReason is one of SerialReasons when no segment ran on two or more
	// lanes, and empty otherwise.
	SerialReason string
}

// laneCounts are a render's segment statistics, from its lanes.
type laneCounts struct {
	segments, parallel, starved int64
	peak                        int
}

// noteLanes fills the lane report. why is SerialGraphLFO or SerialReplay
// when that is known to have kept the render serial, or "".
func (r *Report) noteLanes(requested, jobs, lanes, tracks int, counts laneCounts, why string) {
	l := LaneReport{JobsRequested: requested, Jobs: jobs, Lanes: lanes, LanesPeak: max(1, counts.peak), Segments: counts.segments, ParallelSegments: counts.parallel}
	l.LanesRequested = jobs
	if requested > 0 {
		l.LanesRequested = requested
	}
	if l.ParallelSegments == 0 {
		switch {
		case jobs <= 1:
			l.SerialReason = SerialJobs
		case tracks < 2:
			l.SerialReason = SerialOneTrack
		case why != "":
			l.SerialReason = why
		case counts.starved > 0:
			l.SerialReason = SerialNoProcessor
		default:
			l.SerialReason = SerialLight
		}
	}
	r.LaneReport = l
}
