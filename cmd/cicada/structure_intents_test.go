package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/render"
)

// TestStructureRefusalsAreData checks that a structural refusal reaches
// apply --json as a reason, the field at fault and the uses that block it.
func TestStructureRefusalsAreData(t *testing.T) {
	for _, tc := range []struct {
		name, intent, reason, field, use string
	}{
		{"pattern in use", `{"kind":"deletepattern","pattern":"riff"}`, "pattern_in_use", "pattern", "scene main binding bass = riff (line 9)"},
		{"scene in use", `{"kind":"deletescene","scene":"main"}`, "scene_in_use", "scene", "song entry main*2 (line 10)"},
		{"voice cannot play the pattern", `{"kind":"settrackvoice","track":"lead","voice":"drums"}`, "voice_change_invalid", "voice", ""},
		{"setting the voice lacks", `{"kind":"settrackvoice","track":"bass","voice":"piano"}`, "voice_params_unsupported", "voice", "setting cutoff (line 3)"},
		{"name taken", `{"kind":"rename","entity":"track:bass","name":"lead"}`, "name_taken", "name", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, score := runApplyJSON(t, tc.intent, "--write")
			failure := result.Error
			if err == nil || result.OK || failure == nil || failure.Code != "intent_refused" || failure.Reason != tc.reason || failure.Field != tc.field {
				t.Fatalf("%v %+v", err, failure)
			}
			if tc.use != "" && (len(failure.Uses) != 1 || failure.Uses[0] != tc.use) {
				t.Fatalf("uses %q, want %q", failure.Uses, tc.use)
			}
			if tc.reason == "voice_change_invalid" && len(failure.Uses) == 0 {
				t.Fatal("voice_change_invalid lists no problem")
			}
			if data, _ := os.ReadFile(score); string(data) != applyJSONScore {
				t.Fatal("a refused intent changed the score")
			}
		})
	}
}

// TestRenamesAndUnusedDeletesKeepTheRender renames a track, a pattern and a
// scene of the intent-catalog fixture, which name them in automation, a
// modulation route, scene settings, the song and a MIDI mapping, and deletes
// its unused pattern and scene, in one envelope. The render must stay
// byte-identical.
func TestRenamesAndUnusedDeletesKeepTheRender(t *testing.T) {
	score := catalogProject(t, "rename")
	dir := filepath.Dir(score)
	opts := render.Options{SampleRate: 48000, Bits: 16, Bars: 6, Block: 4096}
	first, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "before.wav"), opts, nil)
	if code != 0 || !first.OK {
		t.Fatalf("render before: %d %s", code, stderr)
	}
	envelope := filepath.Join(dir, "structure.json")
	intents := []string{
		`{"kind":"rename","entity":"track:bass","name":"sub"}`,
		`{"kind":"rename","entity":"pattern:riff","name":"groove"}`,
		`{"kind":"rename","entity":"scene:main","name":"verse"}`,
		`{"kind":"rename","entity":"track:lead","name":"melody"}`,
		`{"kind":"deletepattern","pattern":"fill"}`,
		`{"kind":"deletescene","scene":"outro"}`,
	}
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+strings.Join(intents, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyCommandWithHooks([]string{score, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	edited, err := os.ReadFile(score)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"track bass", "riff", "scene main", "track lead", "lead.", "pattern fill", "scene outro"} {
		if bytes.Contains(edited, []byte(gone)) {
			t.Fatalf("%q is left in the edited score:\n%s", gone, edited)
		}
	}
	second, code, stderr := runRenderJSON(t, "render", score, filepath.Join(dir, "after.wav"), opts, nil)
	if code != 0 || !second.OK {
		t.Fatalf("render after: %d %s", code, stderr)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "before.wav"))
	after, _ := os.ReadFile(filepath.Join(dir, "after.wav"))
	if len(before) == 0 || !bytes.Equal(before, after) {
		t.Fatalf("the render changed: %d bytes before, %d after", len(before), len(after))
	}
}
