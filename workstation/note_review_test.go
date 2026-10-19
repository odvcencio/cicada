package main

import (
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx"
)

func TestNoteReviewShowsOriginalTimingAndPatternDestination(t *testing.T) {
	take := control.Recording{Track: "keys", Pattern: "phrase", Notes: []control.Note{
		{Tick: 43839, EndTick: 44859, Note: 61, Velocity: 101, Input: &midi.NoteTiming{HostNanos: 1000, Source: "arrival", Estimated: true}},
		{Tick: 43200, EndTick: 43440, Note: 60, Velocity: 80},
	}}
	before, _ := json.Marshal(take)
	html := gosx.RenderHTML(noteRecordingReview(take, &project.Pattern{ID: "phrase", Steps: 16, StepTicks: 480}))
	for _, want := range []string{"keys → phrase", "C♯4", "12.2.3 +159 ticks", "1.062 beats · estimated", "Step 12", "0.5 beats per step", "Played at", "Pattern destination", "Estimated lengths include notes released without an input timestamp"} {
		if !strings.Contains(html, want) {
			t.Fatalf("review omitted %q", want)
		}
	}
	after, _ := json.Marshal(take)
	if string(before) != string(after) {
		t.Fatal("review reordered or quantized the original performance")
	}
	if !strings.Contains(html, `role="img"`) || !strings.Contains(html, `<caption>`) {
		t.Fatal("review lacks an accessible chart or note table")
	}
}

func TestNoteReviewMarksDrumAliasesAndKeepsOtherLanesSeparate(t *testing.T) {
	take := control.Recording{Track: "kit", Pattern: "beat", Notes: []control.Note{{Note: 41, Velocity: 90}, {Note: 43, Velocity: 100}, {Note: 36, Velocity: 80}}}
	html := gosx.RenderHTML(noteRecordingReview(take, &project.Pattern{ID: "beat", Kind: "drums", Steps: 16}))
	if strings.Count(html, "shared step") != 2 {
		t.Fatal("drum collisions did not follow native GM lanes")
	}
}

func TestNoteReviewKeepsFailedCheckVisibleAndBlocksApply(t *testing.T) {
	takes, _ := json.Marshal([]control.Recording{{Track: "keys", Pattern: "phrase", Notes: []control.Note{{Note: 60, Velocity: 100}}}})
	v := workspace{Revision: "current", Project: &project.Project{Patterns: []project.Pattern{{ID: "phrase", Steps: 16}}}}
	d := draft{Source: string(takes), Revision: "current", CheckRevision: "current", CheckError: "Multiple notes share step 1."}
	html := gosx.RenderHTML((&studioApp{}).noteTakeReview(v, "csrf", "private-preview", d))
	for _, want := range []string{"C4", "Multiple notes share step 1.", "Your performance is still retained.", "Check current patterns", `value="commit" disabled`} {
		if !strings.Contains(html, want) {
			t.Fatalf("failed review omitted %q", want)
		}
	}
	if strings.Contains(html, `"tick":`) {
		t.Fatal("raw JSON replaced the musical review")
	}
}

func TestPlayedTimingCheckIsIndependentOfGridCollisions(t *testing.T) {
	takes, _ := json.Marshal([]control.Recording{{Track: "keys", Pattern: "phrase", Notes: []control.Note{{Tick: 17, EndTick: 720, Note: 60, Velocity: 82}, {Tick: 19, EndTick: 1220, Note: 64, Velocity: 105}}}})
	v := workspace{Revision: "current", Project: &project.Project{Patterns: []project.Pattern{{ID: "phrase", Steps: 16}}}}
	d := draft{Source: string(takes), Revision: "current", CheckRevision: "current", CheckError: "Multiple notes share step 1.", PlayedCheckRevision: "current"}
	html := gosx.RenderHTML((&studioApp{}).noteTakeReview(v, "csrf", "private-preview", d))
	if !strings.Contains(html, `value="played">Keep played timing`) || !strings.Contains(html, `value="commit" disabled`) {
		t.Fatal("a grid collision blocked keeping the original performance")
	}
	d.PlayedCheckError = "per-note expression is not supported"
	html = gosx.RenderHTML((&studioApp{}).noteTakeReview(v, "csrf", "private-preview", d))
	if !strings.Contains(html, `value="played" disabled`) || !strings.Contains(html, "per-note expression is not supported") {
		t.Fatal("unrepresentable timing was presented as playable")
	}
}
