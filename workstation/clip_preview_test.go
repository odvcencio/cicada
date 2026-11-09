package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestClipPreviewLoopsThePatternAcrossTheClip(t *testing.T) {
	note := func(n uint8) *project.Step { return &project.Step{Note: n} }
	p := &project.Project{Patterns: []project.Pattern{{
		ID: "pulse", Kind: "notes", Steps: 4, GatePercent: 50,
		Data: []*project.Step{note(57), nil, note(64), nil},
	}}}
	// Four steps of 240 ticks loop four times across one 3,840-tick bar.
	html := gosx.RenderHTML(clipPreview(p, "pulse", 3840))
	if got := strings.Count(html, "M"); got != 8 {
		t.Fatalf("want 8 note bars (2 notes x 4 loops), got %d in %s", got, html)
	}
	if !strings.Contains(html, `viewBox="0 0 64 24"`) || !strings.Contains(html, `aria-hidden="true"`) {
		t.Fatalf("unexpected preview frame: %s", html)
	}
	// The higher note sits on the top row.
	notes, loop, rows := previewNotes(&p.Patterns[0])
	if loop != 960 || rows != 8 || notes[1].row != 0 || notes[0].row != 7 {
		t.Fatalf("notes %+v loop %d rows %d", notes, loop, rows)
	}
}

func TestClipPreviewTieHoldsTheWholeChord(t *testing.T) {
	p := &project.Pattern{ID: "pad", Kind: "notes", Steps: 3, GatePercent: 50, Data: []*project.Step{
		{Note: 57, Notes: []int{57, 60, 64}}, {Tie: true}, nil,
	}}
	notes, _, _ := previewNotes(p)
	if len(notes) != 3 {
		t.Fatalf("want 3 chord notes, got %+v", notes)
	}
	for _, n := range notes {
		if n.start != 0 || n.end != 480 {
			t.Fatalf("tied chord note should hold two steps: %+v", n)
		}
	}
}

func TestClipPreviewDrumLanesAndEmptyPatterns(t *testing.T) {
	hit := &project.Step{Note: 36}
	p := &project.Project{Patterns: []project.Pattern{
		{ID: "beat", Kind: "drums", Steps: 2, Lanes: map[string][]*project.Step{"sd": {nil, hit}, "bd": {hit, nil}}},
		{ID: "rest", Kind: "notes", Steps: 2, Data: []*project.Step{nil, nil}},
	}}
	notes, _, rows := previewNotes(&p.Patterns[0])
	if rows != 2 || len(notes) != 2 || notes[0].row != 0 || notes[1].row != 1 {
		t.Fatalf("lanes in name order (bd, sd): %+v rows %d", notes, rows)
	}
	for _, id := range []string{"rest", "missing"} {
		if html := gosx.RenderHTML(clipPreview(p, id, 3840)); strings.Contains(html, "<svg") {
			t.Fatalf("%s should draw nothing, got %s", id, html)
		}
	}
}

func TestClipPreviewBoundsALongBlock(t *testing.T) {
	steps := make([]*project.Step, 16)
	for i := range steps {
		steps[i] = &project.Step{Note: uint8(48 + i)}
	}
	p := &project.Project{Patterns: []project.Pattern{{ID: "run", Kind: "notes", Steps: 16, Data: steps}}}
	// 999 bars of sixteen notes would be 15,984 bars without the bound.
	html := gosx.RenderHTML(clipPreview(p, "run", 999*3840))
	if got := strings.Count(html, "M"); got != previewMaxBars {
		t.Fatalf("want %d bars, got %d", previewMaxBars, got)
	}
}
