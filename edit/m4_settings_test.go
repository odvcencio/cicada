package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Keep semantic validation in the injected host, as production does.
type settingsCompiler struct{ edition int }

func (c settingsCompiler) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	score, ds := notation.Parse(source)
	if c.edition != 0 {
		score, ds = notation.ParseEdition(source, c.edition)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	return project.EditPlan(p, source), nil
}

func settingsIntent(t *testing.T, source []byte, raw string, edition int) (*edits.Result, error) {
	t.Helper()
	var env edits.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+raw+`]}`), &env); err != nil {
		return nil, err
	}
	return edits.Apply(source, env, edits.Options{Compiler: settingsCompiler{edition}, Edition: edition})
}

const settingsScore = "cicada 2\n// authored header\nkey db minor\ntempo // tempo comment\n138.0\ntrack bass acid { cutoff = 700Hz }\npattern p { 1 . c#3 . }\nscene main {\n  bass=p // keep binding\n  bass.cutoff = 900.0Hz // keep point\n}\nsong { main }\n"
const settingsClipScore = "cicada 2\nasset take \"take.wav\" { sha256 = \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\" format = wav frames = 48000 rate = 48000Hz channels = 1 }\nclip hit take { start = 10ms // original region\n end = 100ms gain = -2.0dB } // keep clip comment\ntrack vox audio {}\nscene main { vox = hit }\nsong { main }\n"

func TestSceneAndProjectIntentsPreserveAuthoredSource(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range []struct{ raw, old, next, label string }{
			{`{"kind":"setscenesetting","entity":"setting:main/bass.cutoff","value":"1.25kHz"}`, "900.0Hz", "1250Hz", "Automation · main / bass.cutoff set"},
			{`{"kind":"removescenesetting","entity":"setting:main/bass.cutoff"}`, "bass.cutoff = 900.0Hz", "", "Automation · main / bass.cutoff removed"},
			{`{"kind":"setscenesetting","entity":"setting:main/bass.level","value":-6.25}`, "scene main {", "scene main {\n  bass.level = -6.25dB\n", "Automation · main / bass.level set"},
			{`{"kind":"setprojectsettings","title":"東京 \"Studio\"","tempomilli":127125,"root":"c#","scale":"minor"}`, "// authored header\nkey db minor\ntempo // tempo comment\n138.0", "// authored header\ntitle \"東京 \\\"Studio\\\"\"\nkey db minor\ntempo // tempo comment\n127.125", "Project title, tempo, and key changed"},
		} {
			source := []byte(strings.ReplaceAll(settingsScore, "\n", newline))
			got, err := settingsIntent(t, source, c.raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := bytes.Replace(source, []byte(strings.ReplaceAll(c.old, "\n", newline)), []byte(strings.ReplaceAll(c.next, "\n", newline)), 1)
			if !bytes.Equal(got.Source, want) || got.Label != c.label {
				t.Fatalf("%s: got %q / %q, want %q / %q", c.raw, got.Source, got.Label, want, c.label)
			}
		}
	}
}

func TestClipIntentsPreserveAuthoredUnitsAndComments(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll(settingsClipScore, "\n", newline))
		for _, c := range []struct{ raw, old, next, label string }{
			{`{"kind":"setclipsettings","entity":"clip:hit","start":480,"end":4800,"gaindb":-2,"fadein":48,"fadeout":96}`, "clip hit take {", "clip hit take { fade_in = 48frames fade_out = 96frames ", "Audio region edited · hit"},
			{`{"kind":"addaudiotrack","name":"vocal-b"}`, "scene main", "track vocal-b audio {}\n\nscene main", "Audio track added · vocal-b"},
			{`{"kind":"bindscene","scene":"main","track":"vox","pattern":"off"}`, "vox = hit", "vox = off", "Scene · main / vox assigned off"},
		} {
			got, err := settingsIntent(t, source, c.raw, 2)
			if err != nil {
				t.Fatal(err)
			}
			want := bytes.Replace(source, []byte(strings.ReplaceAll(c.old, "\n", newline)), []byte(strings.ReplaceAll(c.next, "\n", newline)), 1)
			if !bytes.Equal(got.Source, want) || got.Label != c.label {
				t.Fatalf("%s: got %q / %q, want %q / %q", c.raw, got.Source, got.Label, want, c.label)
			}
		}
	}
}

func TestM4SettingsRefusals(t *testing.T) {
	for _, c := range []struct{ source, raw, errorText string }{
		{settingsScore, `{"kind":"setscenesetting","entity":"setting:main/bass.insert","value":"none"}`, "bass.insert cannot be automated at a scene boundary"},
		{settingsScore, `{"kind":"removescenesetting","entity":"setting:main/bass.pan"}`, "scene main has no point for bass.pan"},
		{settingsScore, `{"kind":"setprojectsettings"}`, "choose a title of 1–256 characters and tempo 20–300 BPM"},
		{settingsScore, `{"kind":"setprojectsettings","title":"Test","tempomilli":120000,"root":"db","scale":"minor"}`, "choose a supported key and scale"},
		{settingsClipScore, `{"kind":"setclipsettings","entity":"clip:hit"}`, "choose a valid source region, fades no longer than the region, and gain −60 to +24 dB"},
		{settingsClipScore, `{"kind":"setclipsettings","entity":"clip:hit","start":480,"end":50000}`, "clip end exceeds the source asset"},
		{settingsClipScore, `{"kind":"setclipsettings","entity":"clip:missing","start":480,"end":4800}`, "unknown clip missing"},
		{settingsClipScore, `{"kind":"addaudiotrack","name":"vox"}`, "track vox already exists"},
		{settingsClipScore, `{"kind":"addaudiotrack","name":"bad\ntrack x"}`, "choose a lowercase audio track name in an edition-2 score"},
	} {
		before := []byte(c.source)
		_, err := settingsIntent(t, before, c.raw, 2)
		if err == nil || err.Error() != c.errorText {
			t.Fatalf("%s: %v, want %q", c.raw, err, c.errorText)
		}
		if string(before) != c.source {
			t.Fatal("refusal mutated input")
		}
	}
}
