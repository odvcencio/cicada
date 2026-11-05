package main

import (
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestPatternMachinePreviewAndKeep(t *testing.T) {
	w := &workspace{Revision: "current", SourceRevision: "current-sources", Source: "cicada 2\n", Valid: true, Project: &project.Project{Title: "Test", Key: project.Key{Root: 6, Scale: "minor"}, Tracks: []project.Track{{ID: "lead", Kind: "tine_ep"}, {ID: "beat", Kind: "drums"}}}}
	address, client, edits := testAppWorkspace(t, w)
	page := getPage(t, client, address+"/?panel=patterns&machine=1&track=lead&bars=2&seed=42&density=85&motion=60")
	for _, want := range []string{"Pattern Machine", "Orbit arpeggio", "data-machine-preview", "BAR 01", "BAR 02", "Keep pattern", "name=\"machine_seed\" value=\"42\""} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(page, "BAR 03") || len(*edits) != 0 {
		t.Fatal("preview changed the project or displayed the wrong number of bars")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "source_revision": {"current-sources"}, "action": {"create"}, "newName": {"machine-idea"}, "track": {"lead"}, "length": {"32"}, "starter": {"machine"}, "machine_seed": {"42"}, "machine_bars": {"2"}, "machine_root": {"6"}, "machine_scale": {"minor"}, "machine_density": {"85"}, "machine_motion": {"60"}, "__gosx_return_to": {"/?panel=patterns"}}
	response := post(t, client, address+"/__actions/pattern", form, false, address)
	if response.StatusCode != 303 || len(*edits) != 1 {
		t.Fatalf("keep failed: %d %+v", response.StatusCode, *edits)
	}
	got := (*edits)[0]["machine"].(map[string]any)
	if got["seed"] != float64(42) || got["bars"] != float64(2) || got["root"] != float64(6) || got["scale"] != "minor" {
		t.Fatalf("recipe changed: %+v", got)
	}
	form.Set("machine_bars", "4")
	if response := post(t, client, address+"/__actions/pattern", form, true, address); response.StatusCode != 422 || len(*edits) != 1 {
		t.Fatal("mismatched bars were accepted")
	}
	drums := getPage(t, client, address+"/?panel=patterns&machine=1&track=beat&bars=1")
	for _, want := range []string{"Breakbeat drums", ">bd<", ">sd<", ">ch<"} {
		if !strings.Contains(drums, want) {
			t.Fatalf("drum preview missing %s", want)
		}
	}
}
