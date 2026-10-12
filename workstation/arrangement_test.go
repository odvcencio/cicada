package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestNamedPlacementSessionShowsActualArrangement(t *testing.T) {
	source, err := os.ReadFile("../examples/songs/tidal-glass.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/workspace" {
			_ = json.NewEncoder(w).Encode(workspace{Source: string(source), Filename: "tidal-glass.cicada", Revision: "current", Valid: true, Project: p})
		} else {
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(service.Close)
	b, err := newBackend(service.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	page := getPage(t, app.Client(), app.URL+"/?panel=session")
	for _, want := range []string{`name="gosx-page-cache" content="no-store"`, `aria-label="Named placement timeline"`, `aria-label="Placement editor"`, "24 bars", "1:12 at 80.0 BPM", "9 placements", `data-placement-id="far-in"`, "@5.2.1", "@9.2.1", "shore @1.1.1", "open-water @9.1.1", `data-placement-block="far-in"`, `data-placement-inspector="far-in"`, `aria-label="Resize placement far-in"`, "Update placement", "Duplicate placement", "Add placement", "Edit markers", "Project settings"} {
		if !strings.Contains(page, want) {
			t.Errorf("placement session missing %q", want)
		}
	}
	for _, unwanted := range []string{"0 bars", "Add to arrangement", "Replace scene", "Duplicate block", "Scene launch matrix", "Edit placements and markers in Score"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("placement session offers song control %q", unwanted)
		}
	}
	for _, want := range []string{"Session listening", "Loop section", "Loop an exact range", "Play full song", "Stop listening", "Audition solo", "Audition in song", "Loop solo", "Loop in song", `name="placement" value="far-in"`, `data-gosx-live-bind="listeningStatus"`} {
		if !strings.Contains(page, want) {
			t.Errorf("placement listening missing %q", want)
		}
	}
	if strings.Index(page, `data-placement-id="pad-in"`) > strings.Index(page, `data-placement-id="far-in"`) {
		t.Error("placements are not in chronological order")
	}
	if !strings.Contains(page, `href="/?panel=patterns&amp;pattern=ripple`) {
		t.Error("pattern content is not linked to its editor")
	}
}

func TestPlacementPositionRetainsExactTicks(t *testing.T) {
	if got := placementPosition(480); got != "@1.1.3" {
		t.Fatalf("sixteenth position: %s", got)
	}
	if got := placementPosition(320); got != "320 ticks" {
		t.Fatalf("triplet position was rounded: %s", got)
	}
}

func TestPlacementTimelineShowsOverlappingAudioOnSeparateRows(t *testing.T) {
	placements := []project.Placement{
		{ID: "first", Track: "voice", Content: "take-a", AtTick: 0, LengthTicks: 3840},
		{ID: "second", Track: "voice", Content: "take-b", AtTick: 320, LengthTicks: 3840},
	}
	p := &project.Project{Tracks: []project.Track{{ID: "voice", Kind: "audio"}}, Arrange: &project.Arrangement{Placements: placements}}
	page := gosx.RenderHTML(placementTimeline(p, placements, "second", 4160))
	for _, want := range []string{`data-placement-block="first"`, `data-placement-block="second"`, `data-placement-at="320"`, `top:8px`, `top:76px`, `height:144px`} {
		if !strings.Contains(page, want) {
			t.Fatalf("overlapping audio timeline missing %s: %s", want, page)
		}
	}
}
