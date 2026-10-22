package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPerformancePitchFormsKeepProjectReceiptSelectionAndFailedShift(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "played", Kind: "notes", Transpose: 12, Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}, {Note: 64, Tick: 19, EndTick: 1320, Velocity: 87}}}}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/studio?dock=clip&pattern=played&note=1&track=keys&region=detail")
	section := func(page string) string {
		return regexp.MustCompile(`(?s)<section[^>]*data-performance-pitch="played".*?</section>`).FindString(page)
	}
	for _, want := range []string{"Shape pitch", "Shift notes", "Mirror around E5", `name="pivot" value="64"`, `name="source_revision" value="all-sources"`, `name="track" value="keys"`, `note=1`, `Pitch bends keep their recorded direction`} {
		if !strings.Contains(section(page), want) {
			t.Fatal("missing phrase pitch control", want)
		}
	}
	add := section(getPage(t, client, address+"/studio?dock=clip&pattern=played&note=2&track=keys&region=detail"))
	if !regexp.MustCompile(`<button[^>]*disabled[^>]*>Select a note to mirror</button>`).MatchString(add) {
		t.Fatal("adding a note offered an unrelated mirror center", add)
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"stale"}, "source_revision": {"all-sources"}, "action": {"performance-pitch"}, "pattern": {"played"}, "pitch_operation": {"transpose"}, "semitones": {"-7"}, "__gosx_return_to": {"/studio?dock=clip&pattern=played&note=1&track=keys&region=detail"}}
	r := post(t, client, address+"/__actions/pattern", form, false, address)
	defer r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["source_revision"] != "all-sources" {
		t.Fatal("pitch shaping lost project receipt", r.StatusCode, *edits)
	}
	shape := (*edits)[0]["performancePitch"].(map[string]any)
	if shape["operation"] != "transpose" || shape["semitones"] != float64(-7) {
		t.Fatal("shift changed", shape)
	}
	failed := section(getPage(t, client, address+r.Header.Get("Location")))
	if !strings.Contains(failed, "score changed") || !strings.Contains(failed, `value="-7"`) || !strings.Contains(failed, `role="status"`) {
		t.Fatal("failure hid the shift or retry controls", failed)
	}
	for _, bad := range []string{"0", "49", "-49", "1.5", "up"} {
		form.Set("semitones", bad)
		if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 1 {
			t.Fatal("invalid shift reached backend", bad, r.StatusCode)
		}
	}
	form.Set("pitch_operation", "reflect")
	form.Set("pivot", "64")
	form.Del("semitones")
	if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 409 || len(*edits) != 2 {
		t.Fatal("mirror did not reach backend", r.StatusCode, *edits)
	}
	shape = (*edits)[1]["performancePitch"].(map[string]any)
	if shape["operation"] != "reflect" || shape["pivot"] != float64(64) {
		t.Fatal("mirror center changed", shape)
	}
}

func TestLibraryPerformancePitchOffersLocalVariation(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "motif.played", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}}}}}, Arrange: &project.Arrangement{Placements: []project.Placement{{ID: "return", Track: "keys", Content: "motif.played", LengthTicks: 3840}}}}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=patterns&pattern=motif.played")
	section := regexp.MustCompile(`(?s)<section[^>]*data-performance-pitch="motif.played".*?</section>`).FindString(page)
	if !strings.Contains(section, "keeping the library intact") || !strings.Contains(section, `href="#pattern-variation"`) || strings.Contains(section, "pitch_operation") {
		t.Fatal("library offered a write", section)
	}
}
