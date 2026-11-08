package project

import (
	"fmt"
	"m31labs.dev/cicada/notation"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func TestParseSamplerPitch(t *testing.T) {
	for name, want := range map[string]int{"c4": 60, "d2": 38, "c#2": 37, "eb2": 39, "e2": 40, "bb1": 34, "g9": 127, "c0": 12} {
		if got, ok := ParseSamplerPitch(name); !ok || got != want {
			t.Errorf("%s = %d, %t; want %d", name, got, ok, want)
		}
	}
	for _, name := range []string{"", "h2", "d", "d22", "g#9", "x#2", "kick"} {
		if _, ok := ParseSamplerPitch(name); ok {
			t.Errorf("%q parsed as a pitch", name)
		}
	}
}

func TestCompileKitBindsSamplerLanesAndKeepsOtherKinds(t *testing.T) {
	kit := Kit{ID: "k", Lanes: map[string]string{"sd": "shells.d2", "rs": "shells.c#2", "bd": "builtin.bd", "ch": "model.hat_closed"}}
	bindings, err := CompileKitWithSamplers(kit, nil, map[string]bool{"shells": true}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	sd, rs := bindings[drum.SD], bindings[drum.RS]
	if sd.Kind != engine.KitLaneSampler || sd.Sampler != "shells" || sd.SampleNote != 38 || rs.SampleNote != 37 {
		t.Fatalf("sampler lanes: %+v %+v", sd, rs)
	}
	if bindings[drum.BD].Kind != engine.KitLaneBuiltin || bindings[drum.CH].Kind != engine.KitLaneModeled {
		t.Fatal("a sampler lane changed another lane's binding")
	}
}

func TestCompileKitRefusesBadSamplerLanes(t *testing.T) {
	for _, test := range []struct {
		name     string
		target   string
		samplers map[string]bool
		want     string
	}{
		{"unknown sampler", "nope.d2", map[string]bool{"shells": true}, "no sampler named nope"},
		{"not a pack", "wave.d2", map[string]bool{"wave": false}, "not a sample pack"},
		{"bad pitch", "shells.zz", map[string]bool{"shells": true}, "not a note name"},
	} {
		_, err := CompileKitWithSamplers(Kit{ID: "k", Lanes: map[string]string{"sd": test.target}}, nil, test.samplers, 48000)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v, want %q", test.name, err, test.want)
		}
	}
}

func TestSamplerLaneControlsAreLevelAndPanOnly(t *testing.T) {
	kit := Kit{ID: "k", Lanes: map[string]string{"sd": "shells.d2"}}
	samplers := map[string]bool{"shells": true}
	n := func(v float64, unit string) Value { return Value{Number: &v, Unit: unit} }
	bindings, err := CompileKitTrackWithSamplers(kit, nil, samplers, map[string]Value{"sd_level": n(-9, "db"), "sd_pan": n(.5, "unit")}, 48000)
	if err != nil || bindings[drum.SD].SampleLevelDB != -9 || bindings[drum.SD].SamplePan != .5 {
		t.Fatalf("%+v, %v", bindings[drum.SD], err)
	}
	if _, err = CompileKitTrackWithSamplers(kit, nil, samplers, map[string]Value{"sd_tune": n(1, "unit")}, 48000); err == nil || !strings.Contains(err.Error(), "only level and pan") {
		t.Fatalf("sd_tune: %v", err)
	}
}

const samplerKitLiveSource = `cicada 2
tempo 120
sampler shells { pack = "packs/shells/manifest.json" sha256 = "0000000000000000000000000000000000000000000000000000000000000000" root = c4 voices = 10 }
kit snare_kit { sd = shells.d2 bd = builtin.bd }
track snare snare_kit { sd_level = -3dB }
pattern drop drums steps = 8 { sd: .... x... }
scene main { snare = drop }
song { main }
`

func TestSamplerKitLaneRefusesLiveLevelAndPan(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(samplerKitLiveSource))
	if score == nil {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	for _, path := range []string{"snare.sd_level", "snare.sd_pan"} {
		if _, err := ResolveParameterPath(p, path); err == nil || !strings.Contains(err.Error(), "sampler kit lane level and pan") {
			t.Errorf("%s: live path on a sampler lane resolved: %v", path, err)
		}
	}
	// The builtin lane keeps its live level, and the track block still sets the sampler lane.
	if _, err := ResolveParameterPath(p, "snare.bd_level"); err != nil {
		t.Errorf("builtin lane level refused: %v", err)
	}
	number := -9.0
	p.Scenes[0].Settings = []SceneSetting{{Path: "snare.sd_level", Value: SceneValue{Unit: "db", Number: &number}}}
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "sampler kit lane level and pan") {
		t.Errorf("scene setting on a sampler lane accepted: %v", err)
	}
	p.Scenes[0].Settings = nil
	p.Automation = []AutomationLane{{Path: "snare.sd_pan", Points: []AutomationPoint{{Tick: 0, Value: SceneValue{Unit: "unit", Number: &number}, Shape: "linear"}}}}
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "sampler kit lane level and pan") {
		t.Errorf("automation on a sampler lane accepted: %v", err)
	}
}

func TestKitSamplerVoicesCountOncePerSamplerAgainstTheNativeCap(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(strings.Replace(samplerKitLiveSource, "voices = 10", "voices = 32", 1)))
	if score == nil {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	// One sampler lane and one builtin lane: 32 + 1 voices per track.
	if got := kitVoiceCount(p, p.Kits[0]); got != 33 {
		t.Fatalf("kit voices = %d, want 33", got)
	}
	// Two lanes on the same sampler still share one instrument.
	shared := Kit{ID: "k", Lanes: map[string]string{"sd": "shells.d2", "rs": "shells.c#2", "bd": "builtin.bd"}}
	if got := kitVoiceCount(p, shared); got != 33 {
		t.Fatalf("shared-sampler kit voices = %d, want 33", got)
	}
	base := p.Tracks[0]
	p.Tracks = nil
	for i := 0; i < 7; i++ {
		track := base
		track.ID = fmt.Sprintf("snare%d", i)
		track.Slots = [16]*string{}
		p.Tracks = append(p.Tracks, track)
	}
	p.Scenes[0].Bindings = map[string]string{}
	if err := ValidateProject(p); err != nil {
		t.Fatalf("7 tracks of 33 voices (231) are under the cap: %v", err)
	}
	for i := 7; i < 9; i++ {
		track := base
		track.ID = fmt.Sprintf("snare%d", i)
		track.Slots = [16]*string{}
		p.Tracks = append(p.Tracks, track)
	}
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "allocated voices") {
		t.Fatalf("9 tracks of 33 voices (297) passed the %d-voice cap: %v", engine.MaxVoices, err)
	}
}
