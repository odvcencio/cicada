package project

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func modalPedalScore(t *testing.T, tracks, extra string) (*notation.Score, *Project, []notation.Diagnostic) {
	t.Helper()
	source := "cicada 2\ntempo 120\n" + tracks + "\npattern line notes { gate = 50% c4 . . . }\n" + extra
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", diagnostics)
	}
	_, checked := Check(score)
	diagnostics = append(diagnostics, checked...)
	var p *Project
	if !modalHasError(diagnostics) {
		var projectDiagnostics []notation.Diagnostic
		p, projectDiagnostics = FromScore(score)
		diagnostics = append(diagnostics, projectDiagnostics...)
	}
	return score, p, diagnostics
}

func modalHasError(diagnostics []notation.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

const modalPedalArrange = "arrange { place a vibes line { at = @1.1.1 length = 1bar } }\n"

func TestModalSustainSettingCompilesToThePedal(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  float32
	}{{"on", 1}, {"off", 0}, {"1", 1}, {"0", 0}, {"0.25", .25}, {"50%", .5}} {
		_, p, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { sustain = "+tc.value+" }", modalPedalArrange)
		if p == nil || modalHasError(diagnostics) {
			t.Fatalf("sustain = %s: %+v", tc.value, diagnostics)
		}
		cfg, err := CompileEngine(p, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Track[0].Kind != engine.VoiceModal || cfg.Track[0].PianoSustain != tc.want {
			t.Fatalf("sustain = %s compiled to kind %d, pedal %g, want %g", tc.value, cfg.Track[0].Kind, cfg.Track[0].PianoSustain, tc.want)
		}
	}
	_, p, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { level = -6dB octave = 3 }", modalPedalArrange)
	if p == nil {
		t.Fatalf("a track without the setting: %+v", diagnostics)
	}
	if cfg, err := CompileEngine(p, 48000, 128); err != nil || cfg.Track[0].PianoSustain != 0 {
		t.Fatalf("a track without the setting has a pedal: %v", err)
	}
}

func TestModalSustainRejectsValuesThatAreNotAPedal(t *testing.T) {
	for _, value := range []string{"2", "-0.5", "1hz", "6dB", "maybe", "\"on\""} {
		_, _, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { sustain = "+value+" }", modalPedalArrange)
		found := false
		for _, d := range diagnostics {
			found = found || d.Severity == "error" && d.Code == "CICADA-PARAM" && strings.Contains(d.Message, "modal sustain")
		}
		if !found {
			t.Errorf("sustain = %s was accepted: %+v", value, diagnostics)
		}
	}
	// The pedal is the only setting a modal track gained.
	for _, setting := range []string{"cutoff = 200Hz", "decay = 1s", "voices = 2"} {
		_, _, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { "+setting+" }", modalPedalArrange)
		if !modalHasError(diagnostics) {
			t.Errorf("%s was accepted", setting)
		}
	}
	// Its meaning in the semantic project is checked too, for JSON input.
	_, p, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { sustain = on }", modalPedalArrange)
	if p == nil {
		t.Fatalf("sustain = on: %+v", diagnostics)
	}
	one := 1.0
	p.Tracks[0].Params["sustain"] = Value{Unit: "unit", Number: &one}
	if err := ValidateProject(p); err != nil {
		t.Fatal(err)
	}
	two := 2.0
	p.Tracks[0].Params["sustain"] = Value{Unit: "unit", Number: &two}
	if err := ValidateProject(p); err == nil {
		t.Fatal("a project with sustain 2 passed validation")
	}
	delete(p.Tracks[0].Params, "sustain")
	p.Tracks[0].Params["cutoff"] = Value{Unit: "unit", Number: &one}
	if err := ValidateProject(p); err == nil {
		t.Fatal("a project with a cutoff on a modal track passed validation")
	}
}

func TestModalSustainRoundTripsThroughProjectJSONAndSource(t *testing.T) {
	_, p, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { sustain = on }", modalPedalArrange)
	if p == nil {
		t.Fatalf("sustain = on: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := CanonicalJSON(decoded); err != nil || !bytes.Equal(encoded, again) {
		t.Fatal("JSON changed")
	}
	text, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "sustain = on") {
		t.Fatalf("source lost the pedal:\n%s", text)
	}
	round, diagnostics := notation.Parse(text)
	if round == nil || modalHasError(diagnostics) {
		t.Fatal(diagnostics)
	}
	again, diagnostics := FromScore(round)
	if again == nil {
		t.Fatal(diagnostics)
	}
	if cfg, err := CompileEngine(again, 48000, 128); err != nil || cfg.Track[0].PianoSustain != 1 {
		t.Fatalf("round trip lost the pedal: %v", err)
	}
}

func TestModalSustainSceneSettingsAutomationAndLiveAddresses(t *testing.T) {
	tracks := "track vibes model_vibraphone { }\ntrack pad model_marimba { }\ntrack bass acid { }"
	extra := `automate vibes.sustain {
  @1.1.1 1
  @1.3.1 0 step
  @1.3.2 1 step
}
scene hold { vibes = line vibes.sustain = 1 }
scene free { vibes = line vibes.sustain = 0 }
song { hold free }
`
	_, p, diagnostics := modalPedalScore(t, tracks, extra)
	if p == nil || modalHasError(diagnostics) {
		t.Fatalf("%+v", diagnostics)
	}
	resolved, err := ResolveParameterPath(p, "vibes.sustain")
	if err != nil || resolved.ID != kernel.ParamPianoSustain || resolved.Track != 0 || resolved.Descriptor.ID != "piano.sustain" {
		t.Fatalf("vibes.sustain resolved to %+v (%v)", resolved, err)
	}
	if _, err := ResolveParameterPath(p, "pad.sustain"); err != nil {
		t.Fatalf("every modal profile takes the setting: %v", err)
	}
	if _, err := ResolveParameterPath(p, "bass.sustain"); err == nil {
		t.Fatal("an acid track took a pedal")
	}
	scene, err := CompileSceneSettings(p, p.Scenes[0])
	if err != nil || len(scene) != 1 || scene[0].ID != kernel.ParamPianoSustain || scene[0].Track != 0 || scene[0].Value != 1 {
		t.Fatalf("scene settings %+v (%v)", scene, err)
	}
	controls, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	var pedal []float32
	var ticks []int64
	for _, c := range controls {
		if c.Index == uint16(kernel.ParamPianoSustain) && c.Track == 0 {
			pedal = append(pedal, math.Float32frombits(c.Arg0))
			ticks = append(ticks, c.Tick)
		}
	}
	// Step points hold the value and change it on their own tick: a lift at
	// beat 3 and a new press a sixteenth later, 240 ticks on.
	want := []float32{1, 0, 1}
	if len(pedal) < 3 || pedal[0] != 1 || pedal[1] != 0 || pedal[2] != 1 || ticks[1] != 1920 || ticks[2] != 2160 {
		t.Fatalf("pedal controls %v at ticks %v, want %v", pedal, ticks, want)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Automation) == 0 || len(cfg.Scenes) != 2 {
		t.Fatalf("engine config lost the controls: %d automation, %d scenes", len(cfg.Automation), len(cfg.Scenes))
	}
	addresses := map[string]ParamAddress{}
	for _, a := range ParamAddresses(p) {
		addresses[a.Address] = a
	}
	for _, name := range []string{"vibes.sustain", "pad.sustain"} {
		if a, ok := addresses[name]; !ok || a.Param != "piano.sustain" || a.Track == "" || a.Value != float64(0) {
			t.Fatalf("live address %s = %+v", name, a)
		}
	}
	if _, ok := addresses["bass.sustain"]; ok {
		t.Fatal("an acid track has a live pedal address")
	}
	if _, ok := addresses["vibes.level"]; !ok {
		t.Fatal("the mixer rows of a modal track went missing")
	}
}

func TestModalSceneSettingOutsideTheRangeIsReported(t *testing.T) {
	_, _, diagnostics := modalPedalScore(t, "track vibes model_vibraphone { }", "scene hold { vibes = line vibes.sustain = 2 }\nsong { hold }\n")
	for _, d := range diagnostics {
		if d.Severity == "error" && strings.Contains(d.Message, "maximum 1") {
			return
		}
	}
	t.Fatalf("sustain = 2 in a scene was accepted: %+v", diagnostics)
}
