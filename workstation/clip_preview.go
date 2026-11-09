package main

import (
	"fmt"
	"slices"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

// Clip previews draw a pattern's notes or drum hits inside an Arrange clip. A
// preview is one SVG path of small bars, looped across the clip's length, so a
// song with many clips stays cheap to render and to reconcile.

const (
	previewHeight   = 24.0
	previewTickUnit = 60.0 // ticks per viewBox unit along x
	// previewMaxBars bounds one clip's path. A long block (a 999-bar song entry)
	// would otherwise repeat its loop thousands of times into a clip that is
	// only a few hundred pixels wide.
	previewMaxBars = 600
)

// clipPreview returns the preview for pattern id played for lengthTicks, or an
// empty fragment when there is nothing to draw.
func clipPreview(p *project.Project, id string, lengthTicks int64) gosx.Node {
	var pattern *project.Pattern
	for i := range p.Patterns {
		if p.Patterns[i].ID == id {
			pattern = &p.Patterns[i]
			break
		}
	}
	if pattern == nil || lengthTicks <= 0 {
		return gosx.Fragment()
	}
	d := previewPath(pattern, lengthTicks)
	if d == "" {
		return gosx.Fragment()
	}
	return gosx.El("svg", gosx.Attrs(
		gosx.Attr("class", "clip-preview"),
		gosx.Attr("viewBox", fmt.Sprintf("0 0 %g %g", float64(lengthTicks)/previewTickUnit, previewHeight)),
		gosx.Attr("preserveAspectRatio", "none"),
		gosx.Attr("aria-hidden", "true"),
		gosx.Attr("focusable", "false"),
	), gosx.El("path", gosx.Attrs(gosx.Attr("d", d))))
}

// previewNote is one bar of the preview in pattern ticks and a row from 0 (top).
type previewNote struct {
	start, end int64
	row        float64
}

// previewPath draws pattern looped over lengthTicks as rectangles in one path.
func previewPath(pattern *project.Pattern, lengthTicks int64) string {
	notes, loop, rows := previewNotes(pattern)
	if len(notes) == 0 || loop <= 0 {
		return ""
	}
	rowHeight := previewHeight / float64(max(rows, 1))
	barHeight := min(rowHeight*0.8, 4.0)
	var b strings.Builder
	drawn := 0
	for offset := int64(0); offset < lengthTicks && drawn < previewMaxBars; offset += loop {
		for _, note := range notes {
			start := offset + note.start
			if start >= lengthTicks || drawn >= previewMaxBars {
				continue
			}
			drawn++
			end := min(offset+note.end, lengthTicks)
			x := float64(start) / previewTickUnit
			w := max(float64(end-start)/previewTickUnit, 0.5)
			y := note.row*rowHeight + (rowHeight-barHeight)/2
			fmt.Fprintf(&b, "M%.4g %.4gh%.4gv%.4gh%.4gz", x, y, w, barHeight, -w)
		}
	}
	return b.String()
}

// previewNotes lists a pattern's notes with their rows, the loop length in
// ticks and the row count. Melodic rows span the pattern's own pitch range,
// highest first; drum rows are the lanes in name order.
func previewNotes(pattern *project.Pattern) ([]previewNote, int64, int) {
	if perf := pattern.Performance; perf != nil {
		low, high := 127, 0
		for _, note := range perf.Notes {
			low, high = min(low, int(note.Note)), max(high, int(note.Note))
		}
		var notes []previewNote
		for _, note := range perf.Notes {
			notes = append(notes, previewNote{start: note.Tick, end: note.EndTick, row: float64(high - int(note.Note))})
		}
		return notes, perf.LengthTicks, high - low + 1
	}
	step := int64(pattern.StepTicks)
	if step == 0 {
		step = 240
	}
	loop := int64(pattern.Steps) * step
	gate := max(step*int64(max(pattern.GatePercent, 10))/100, step/4)
	if pattern.Kind == "drums" {
		lanes := make([]string, 0, len(pattern.Lanes))
		for name := range pattern.Lanes {
			lanes = append(lanes, name)
		}
		slices.Sort(lanes)
		var notes []previewNote
		for row, name := range lanes {
			for i, hit := range pattern.Lanes[name] {
				if hit != nil {
					notes = append(notes, previewNote{start: int64(i) * step, end: int64(i)*step + step/2, row: float64(row)})
				}
			}
		}
		return notes, loop, len(lanes)
	}
	pitches := func(s *project.Step) []int {
		if len(s.Notes) > 0 {
			return s.Notes
		}
		return []int{int(s.Note)}
	}
	low, high := 127, 0
	for _, s := range pattern.Data {
		if s == nil {
			continue
		}
		for _, pitch := range pitches(s) {
			low, high = min(low, pitch), max(high, pitch)
		}
	}
	var notes []previewNote
	held := 0 // the first note of the last sounding step
	for i, s := range pattern.Data {
		if s == nil {
			continue
		}
		// A tie ("-") holds the previous step's notes through this step.
		if s.Tie {
			for j := held; j < len(notes); j++ {
				notes[j].end = int64(i+1) * step
			}
			continue
		}
		held = len(notes)
		for _, pitch := range pitches(s) {
			notes = append(notes, previewNote{start: int64(i) * step, end: int64(i)*step + gate, row: float64(high - pitch)})
		}
	}
	return notes, loop, high - low + 1
}
