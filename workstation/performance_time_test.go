package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPerformanceTimeFormsUseProjectReceiptsAndRetainFailures(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "played", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}}}}}}
	address, client, edits := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "all-sources", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=patterns&pattern=played&note=0")
	section := func(page string) string {
		return regexp.MustCompile(`(?s)<section[^>]*data-performance-time="played".*?</section>`).FindString(page)
	}
	for _, want := range []string{"Shape time", "Half as long · 2 beats", "Two thirds as long · 8/3 beats", "Twice as long · 8 beats", `name="source_revision" value="all-sources"`} {
		if !strings.Contains(section(page), want) {
			t.Fatal("missing phrase time control", want)
		}
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"stale"}, "source_revision": {"all-sources"}, "action": {"performance-scale"}, "pattern": {"played"}, "scale": {"2/3"}, "__gosx_return_to": {"/?panel=patterns&pattern=played&note=0"}}
	r := post(t, client, address+"/__actions/pattern", form, false, address)
	defer r.Body.Close()
	if r.StatusCode != 303 || len(*edits) != 1 || (*edits)[0]["source_revision"] != "all-sources" {
		t.Fatal("time scale lost project receipt", r.StatusCode, *edits)
	}
	scale := (*edits)[0]["performanceScale"].(map[string]any)
	if scale["numerator"] != float64(2) || scale["denominator"] != float64(3) {
		t.Fatal("scale ratio changed", scale)
	}
	failed := section(getPage(t, client, address+r.Header.Get("Location")))
	if !strings.Contains(failed, "score changed") || !strings.Contains(failed, `role="status"`) || !strings.Contains(failed, "Twice as long") {
		t.Fatal("native failure hid the retry controls")
	}
	for _, bad := range []string{"1/0", "0/1", "17/1", "half", "2/3/4"} {
		form.Set("scale", bad)
		if r := post(t, client, address+"/__actions/pattern", form, true, address); r.StatusCode != 422 || len(*edits) != 1 {
			t.Fatal("invalid ratio reached the backend", bad)
		}
	}
}

func TestLibraryPerformanceTimeLinksToIndependentVariations(t *testing.T) {
	p := &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "keys", Kind: "piano"}}, Patterns: []project.Pattern{{ID: "motif.played", Kind: "notes", Performance: &project.Performance{LengthTicks: 3840, Notes: []project.PerformedNote{{Note: 60, Tick: 17, EndTick: 720, Velocity: 90}}}}}, Arrange: &project.Arrangement{Placements: []project.Placement{{ID: "return", Track: "keys", Content: "motif.played", LengthTicks: 3840}}}}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", Valid: true, Project: p})
	page := getPage(t, client, address+"/?panel=patterns&pattern=motif.played")
	section := regexp.MustCompile(`(?s)<section[^>]*data-performance-time="motif.played".*?</section>`).FindString(page)
	if !strings.Contains(section, "keeping the library intact") || !strings.Contains(section, `href="#pattern-variation"`) || strings.Contains(section, "performance-scale") {
		t.Fatal("library performance offered a write instead of a local variation", section)
	}
}
