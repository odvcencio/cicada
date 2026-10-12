package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
)

func TestLivePlanMapsStableIDsToEngineIDs(t *testing.T) {
	_, path := studioTestHandler(t)
	plan, err := buildLivePlan(path, []byte(studioScore), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if track, ok := plan.tracks["bass"]; !ok || track != 0 {
		t.Fatalf("track map %+v", plan.tracks)
	}
	if ref, ok := plan.patterns["pulse"]; !ok || ref.track != 0 {
		t.Fatalf("pattern map %+v", plan.patterns)
	}
	key := paramKey{entity: "track:bass", id: kernel.ParamMixGain}
	if got := plan.parameterPaths["bass.level"]; got != key {
		t.Fatalf("parameter path resolved to %+v", got)
	}
	if value, ok := plan.params[key]; !ok || value != -6 {
		t.Fatalf("gain %g, present %v", value, ok)
	}
	if plan.revision != studioRevision([]byte(studioScore)) {
		t.Fatal("plan revision must be the committed revision")
	}
}

func TestPlanDiffUsesLiveParameterCommandsAndKeepsStaticControlsStructural(t *testing.T) {
	_, path := studioTestHandler(t)
	source := strings.Replace(studioScore, "track bass acid {}", "track bass acid { cutoff=900Hz }", 1)
	source = "fx room delay { feedback=0.35 }\n" + source
	before, err := buildLivePlan(path, []byte(source), liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to string
		id       kernel.ParamID
	}{
		{"900Hz", "1200Hz", kernel.ParamAcidCutoff},
		{"feedback=0.35", "feedback=0.5", kernel.ParamFxDelayFeedback},
	} {
		after, err := buildLivePlan(path, []byte(strings.Replace(source, tc.from, tc.to, 1)), liveSampleRate)
		if err != nil {
			t.Fatal(err)
		}
		delta := planDiff(before, after)
		if delta.structural || len(delta.params) != 1 || delta.params[0].id != tc.id {
			t.Fatalf("live parameter diff %+v", delta)
		}
	}
	after, err := buildLivePlan(path, []byte(strings.Replace(source, "cutoff=900Hz", "cutoff=900Hz filter=ladder", 1)), liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	if !planDiff(before, after).structural {
		t.Fatal("static acid filter change was patched")
	}
}

func TestLivePlanRenumbersPlacementsButKeepsStableIDs(t *testing.T) {
	source := "cicada 2\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\narrange {\n"
	for i, id := range []string{"a", "b", "c"} {
		source += placementLine(id, i+1)
	}
	source += "}\n"
	path := filepath.Join(t.TempDir(), "arrange.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildLivePlan(path, []byte(source), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	after, err := buildLivePlan(path, []byte(strings.Replace(source, placementLine("a", 1), "", 1)), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if plan.placements["b"].id != 2 || after.placements["b"].id != 1 {
		t.Fatal("engine IDs did not follow source order")
	}
	if plan.placements["b"].event.Tick != after.placements["b"].event.Tick {
		t.Fatal("placement b moved when a was deleted")
	}
}

func placementLine(id string, bar int) string {
	return fmt.Sprintf("place %s bass pulse { at = @%d.1.1 length = 1bar }\n", id, bar)
}

func TestPlanDiffPatchesMixerValuesAndFlagsStructure(t *testing.T) {
	_, path := studioTestHandler(t)
	before, err := buildLivePlan(path, []byte(studioScore), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, from, to string
		structural     bool
	}{
		{"gain", "track bass acid {}", "track bass acid { level = -3dB }", false},
		{"added", "track bass acid {}", "track bass acid {}\ntrack lead acid {}", true},
		{"step", "1 . 5 .", "1 . 7 .", true},
		{"binding", "bass=pulse", "bass=off", true},
		{"tempo", "title", "tempo 120\ntitle", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after, err := buildLivePlan(path, []byte(strings.Replace(studioScore, tc.from, tc.to, 1)), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			diff := planDiff(before, after)
			if diff.structural != tc.structural {
				t.Fatalf("diff %+v", diff)
			}
			if tc.name == "gain" && (len(diff.params) != 1 || diff.params[0].id != kernel.ParamMixGain || diff.params[0].value != -3) {
				t.Fatalf("mixer diff %+v", diff)
			}
			if tc.name == "added" && diff.reason != "track added: lead" {
				t.Fatalf("structural diff %+v", diff)
			}
		})
	}
}
