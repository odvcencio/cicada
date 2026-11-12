package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/render"
)

// parseJobs reads --jobs: absent is the serial render (render.Options.Jobs
// zero), auto is render.AutoJobs, and otherwise a count of at least 1.
func parseJobs(text string) (int, error) {
	switch text {
	case "":
		return 0, nil
	case "auto":
		return render.AutoJobs, nil
	}
	jobs, err := strconv.Atoi(text)
	if err != nil || jobs < 1 {
		return 0, &jobsArgError{text}
	}
	return jobs, nil
}

// jobsArgError is a --jobs value that is neither auto nor a count of at
// least 1. The text commands print it, as the JSON ones do, instead of the
// general usage line.
type jobsArgError struct{ text string }

func (e *jobsArgError) Error() string {
	return fmt.Sprintf("--jobs takes auto or a count of at least 1, not %q", e.text)
}

// laneSummary is the summary line's account of the render's lanes.
func laneSummary(l render.LaneReport) string {
	text := fmt.Sprintf("jobs %d, lanes %d", l.Jobs, l.Lanes)
	if l.SerialReason != "" {
		return text + " (serial: " + l.SerialReason + ")"
	}
	return text + fmt.Sprintf(" (peak %d, %d of %d segments on two or more lanes)", l.LanesPeak, l.ParallelSegments, l.Segments)
}

// RenderLanesJSON is the lane report of render --json and stems --json. It
// is exported only so the JSON decoder can fill it inside renderDocument.
type RenderLanesJSON struct {
	Jobs             int    `json:"jobs"`
	JobsAuto         bool   `json:"jobs_auto,omitempty"`
	LanesRequested   int    `json:"lanes_requested"`
	Lanes            int    `json:"lanes"`
	LanesPeak        int    `json:"lanes_peak"`
	Segments         int64  `json:"segments"`
	ParallelSegments int64  `json:"parallel_segments"`
	SerialReason     string `json:"serial_reason,omitempty"`
}

func lanesJSON(l render.LaneReport) *RenderLanesJSON {
	return &RenderLanesJSON{Jobs: l.Jobs, JobsAuto: l.JobsRequested == render.AutoJobs, LanesRequested: l.LanesRequested, Lanes: l.Lanes, LanesPeak: l.LanesPeak, Segments: l.Segments, ParallelSegments: l.ParallelSegments, SerialReason: l.SerialReason}
}
