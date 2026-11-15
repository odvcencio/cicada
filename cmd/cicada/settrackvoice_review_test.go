package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/testpack"
	"m31labs.dev/cicada/notation"
)

// applyVoiceChange writes score (and a pack, when withPack is set: it covers
// MIDI 48 to 72, as sampler sound) into a project, applies one intent with
// cicada apply --json --write, and returns whether the score file changed.
func applyVoiceChange(t *testing.T, score, intent string, withPack bool) (applyJSONResult, error, string, bool) {
	t.Helper()
	dir := t.TempDir()
	if withPack {
		score = strings.Replace(score, "tempo 120\n", "tempo 120\n"+testpack.Write(t, dir, testpack.Options{}).Sampler("sound", 2), 1)
	}
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(score), 0o600); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(dir, "intents.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"revision":"`+edits.Revision([]byte(score))+`","intents":[`+intent+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := applyCommandWithHooks([]string{path, envelope, "--json", "--write"}, &stdout, nil)
	var result applyJSONResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("%v\n%s", decodeErr, stdout.String())
	}
	after, _ := os.ReadFile(path)
	return result, err, string(after), string(after) != score
}

// The cases of the #365 review. A voice change that changes what plays is
// refused with voice_changes_sound and every change in uses, whether or not
// the table of what voices do knows the cause: the compiled music before and
// after is compared too.
func TestSetTrackVoiceListsEveryChangeTheReviewFound(t *testing.T) {
	for _, tc := range []struct {
		name, score, intent, reason string
		uses                        []string
	}{
		{"a pitch without an octave takes it from the voice (review repro A)",
			"cicada 2\ntempo 120\nkey c minor\ntrack lead acid {}\npattern riff { 1 3 5 3 }\nscene main { lead = riff }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"lead","voice":"model_marimba"}`, "voice_changes_sound",
			[]string{"pattern riff: 4 notes sound 24 semitones higher", "write the octave, such as e2, to keep the pitch on model_marimba"}},
		{"an instrument's octave (review repro C)",
			"cicada 2\ntempo 120\nkey c minor\ninstrument glow { octave = 3  voice mono { out = saw(pitch) * env(gate, 200ms) } }\ntrack lead glow {}\npattern hook { 1 3 5 3 }\nscene main { lead = hook }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"lead","voice":"acid"}`, "voice_changes_sound",
			[]string{"pattern hook: 4 notes sound 12 semitones lower"}},
		{"a kit lane on an instrument that does not read velocity (review finding 3)",
			"cicada 2\ntempo 120\ninstrument thump { voice mono { out = sine(pitch) * env(gate, 120ms) } }\nkit soft { bd = thump sd = builtin.sd }\ntrack beat drums {}\npattern groove drums { bd: x9 . x2 . x9 . x2 . sd: . . x . . . x . }\nscene main { beat = groove }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"beat","voice":"soft"}`, "voice_changes_sound",
			[]string{"pattern groove lane bd (line 6): hit velocity: drums plays it, soft ignores it"}},
		{"another track's polyphony (review finding 4)",
			"cicada 2\ntempo 120\ninstrument pad { voice poly { out = saw(pitch) * adsr(gate, 5ms, 100ms, 0.8, 2000ms) } }\ninstrument organ { voice poly { out = sine(pitch) * env(gate, 300ms) } }\ntrack keys pad {}\ntrack lead organ {}\npattern wash notes { c3 e3 g3 b3 d4 f4 a4 c5 }\npattern stabs notes { [a2 c3 e3] . . . [g2 b2 d3] . . . }\nscene main { keys = wash lead = stabs }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"lead","voice":"pad"}`, "voice_changes_sound",
			[]string{"track keys plays 4 voices instead of 8"}},
		{"a compressor keyed to the track",
			"cicada 2\ntempo 120\nfx duck comp { sidechain = lead }\nbus music { insert = duck }\ntrack lead acid {}\ntrack pad piano {}\npattern line { a1 . c2 . }\npattern hold { a3 - - - }\nscene main { lead = line pad = hold }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"lead","voice":"piano"}`, "voice_changes_sound",
			[]string{"effect duck sidechain (line 3) follows track lead's sound"}},
		{"a note outside a pack's zones (review finding 2)",
			"cicada 2\ntempo 120\ntrack lead acid {}\npattern melody { c1 . e4 . }\nscene main { lead = melody }\nsong { main }\n",
			`{"kind":"settrackvoice","track":"lead","voice":"sound"}`, "voice_change_invalid",
			[]string{"outside every zone of the pack for sampler sound"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			result, err, _, changed := applyVoiceChange(t, tc.score, tc.intent, strings.Contains(tc.intent, `"sound"`))
			t.Logf("settrackvoice, refused, in %s", time.Since(started).Round(time.Millisecond))
			if err == nil || result.Written || changed || result.Error == nil || result.Error.Reason != tc.reason {
				t.Fatalf("%v %+v", err, result.Error)
			}
			joined := strings.Join(result.Error.Uses, "\n")
			for _, use := range tc.uses {
				if !strings.Contains(joined, use) {
					t.Errorf("uses lack %q:\n%s", use, joined)
				}
			}
		})
	}
}

// With acceptchanges the octave move of repro A is written, and the warnings
// say what changed.
func TestSetTrackVoiceAcceptsAnOctaveMoveWhenAsked(t *testing.T) {
	score := "cicada 2\ntempo 120\nkey c minor\ntrack lead acid {}\npattern riff { 1 3 5 3 }\nscene main { lead = riff }\nsong { main }\n"
	result, err, after, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"model_marimba","acceptchanges":true}`, false)
	if err != nil || !result.Written || !strings.Contains(after, "track lead model_marimba {}") || len(result.Intents) != 1 || !strings.Contains(strings.Join(result.Intents[0].Warnings, "\n"), "4 notes sound 24 semitones higher") {
		t.Fatalf("%v %+v %+v", err, result.Error, result.Intents)
	}
}

// Drum patterns that set gate, or transpose = 0, which the language allows,
// are compared like any other input, so a drums-to-kit change goes through
// (review finding 6: they were refused as unmodeled, with no way through).
func TestSetTrackVoiceComparesDrumGateAndTranspose(t *testing.T) {
	score := "cicada 2\ntempo 120\nkit k { bd = builtin.bd sd = builtin.sd }\ntrack beat drums {}\npattern groove drums { gate = 50% transpose = 0 bd: x . x . sd: . x . x }\nscene main { beat = groove }\nsong { main }\n"
	result, err, after, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"beat","voice":"k"}`, false)
	if err != nil || !result.Written || !strings.Contains(after, "track beat k {}") {
		t.Fatalf("%v %+v", err, result.Error)
	}
}

func TestVoiceDropChecksPackZones(t *testing.T) {
	score := "cicada 2\ntempo 120\ntrack lead acid { cutoff = 700Hz }\npattern melody { c1 . e4 . }\nscene main { lead = melody }\nsong { main }\n"
	result, err, _, changed := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"sound","dropparams":true}`, true)
	if err == nil || changed || result.Error == nil || result.Error.Reason != "voice_change_invalid" || !strings.Contains(strings.Join(result.Error.Uses, " "), "outside every zone") {
		t.Fatalf("%v %+v", err, result.Error)
	}
}

func TestVoiceChangeReferences(t *testing.T) {
	for _, tc := range []struct{ declaration, want string }{
		{"fx duck comp { sidechain = \"lead\" }\nbus music { insert = duck }", "effect duck sidechain"},
		{`envelope follow { source = lead attack = 10ms release = 200ms }`, "envelope follow source"},
	} {
		score := "cicada 2\ntempo 120\n" + tc.declaration + "\ntrack lead acid {}\npattern melody { a1 . c2 . }\nscene main { lead = melody }\nsong { main }\n"
		result, err, _, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"piano"}`, false)
		if err == nil || result.Error == nil || result.Error.Reason != "voice_changes_sound" || !strings.Contains(strings.Join(result.Error.Uses, " "), tc.want) {
			t.Fatalf("%s: %v %+v", tc.want, err, result.Error)
		}
	}
}

func TestVoiceAcceptanceRequiresRevision(t *testing.T) {
	for _, intent := range []string{
		`{"kind":"settrackvoice","track":"lead","voice":"piano","acceptchanges":true}`,
		`{"kind":"settrackvoice","track":"lead","voice":"piano","dropparams":true}`,
		`{"kind":"insertlibraryitem","track":"lead","importpath":"std/synth","reference":"synth.glassbass","acceptchanges":true}`,
	} {
		result, err, _ := runApplyJSON(t, intent, "--write")
		if err == nil || result.Error == nil || result.Error.Reason != "revision_required" || result.Error.Field != "revision" {
			t.Fatalf("%s: %v %+v", intent, err, result.Error)
		}
	}
}

func TestVoiceDropKeepsFormatAndNamesComments(t *testing.T) {
	score := "cicada 2\ntempo 120\ntrack lead acid {\n  // first\n  // second\n  cutoff = 700Hz\n}\npattern melody { a1 . c2 . }\nscene main { lead = melody }\nsong { main }\n"
	result, err, after, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"piano","dropparams":true,"acceptchanges":true}`, false)
	if err != nil || !strings.Contains(after, "track lead piano {}") || len(result.Intents) != 1 || !strings.Contains(strings.Join(result.Intents[0].Warnings, " "), "// first") || !strings.Contains(strings.Join(result.Intents[0].Warnings, " "), "// second") {
		t.Fatalf("%v %+v %s", err, result.Error, after)
	}
}

func TestStudioVoiceChangeUndo(t *testing.T) {
	isolateState(t)
	score := "cicada 2\ntempo 120\ntrack lead acid {}\npattern melody { 1 3 5 3 }\nscene main { lead = melody }\nsong { main }\n"
	_, handler, path := proposalFixture(t, score)
	env := edits.Envelope{Version: 1, Revision: studioRevision([]byte(score)), Intents: []edits.Intent{&edits.SetTrackVoice{Track: "lead", Voice: "model_marimba", AcceptChanges: true}}}
	response := studioCall(t, handler, "/api/intents", env)
	if response.Code != 200 {
		t.Fatalf("swap: %d %s", response.Code, response.Body.String())
	}
	changed, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(changed), "track lead model_marimba") {
		t.Fatalf("swap: %s %v", changed, err)
	}
	undo := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(changed)})
	restored, err := os.ReadFile(path)
	if undo.Code != 200 || err != nil || !bytes.Equal(restored, []byte(score)) {
		t.Fatalf("undo: %d %s %v", undo.Code, restored, err)
	}
}

func TestVoiceChangeReportsInvalidSettingValue(t *testing.T) {
	score := "cicada 2\ntempo 120\ntrack lead model_marimba { sustain = on }\npattern melody { a3 . c4 . }\nscene main { lead = melody }\nsong { main }\n"
	result, err, _, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"piano"}`, false)
	if err == nil || result.Error == nil || result.Error.Reason != "voice_change_invalid" || !strings.Contains(strings.Join(result.Error.Uses, " "), "piano sustain must be a unitless value") {
		t.Fatalf("%v %+v", err, result.Error)
	}
}

func TestVoiceChangeRefusesUnmodeledInput(t *testing.T) {
	// Simulate a newly added input whose classification has not caught up.
	key := "row bend"
	previous := notation.PatternSyntax[key]
	delete(notation.PatternSyntax, key)
	t.Cleanup(func() { notation.PatternSyntax[key] = previous })
	score := "cicada 2\ntempo 120\ninstrument organ { voice mono { out = sine(pitch) * env(gate, 300ms) } }\ntrack lead acid {}\npattern melody { c4 . e4 . bend: 0ct . 0ct . }\nscene main { lead = melody }\nsong { main }\n"
	result, err, _, _ := applyVoiceChange(t, score, `{"kind":"settrackvoice","track":"lead","voice":"organ"}`, false)
	if err == nil || result.Error == nil || result.Error.Reason != "voice_input_unmodeled" || !strings.Contains(strings.Join(result.Error.Uses, " "), "row bend") {
		t.Fatalf("%v %+v", err, result.Error)
	}
}
