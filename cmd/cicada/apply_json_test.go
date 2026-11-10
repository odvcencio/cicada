package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

type applyJSONResult struct {
	Schema         string            `json:"schema"`
	OK             bool              `json:"ok"`
	Written        bool              `json:"written"`
	DryRun         bool              `json:"dry_run"`
	Changed        bool              `json:"changed"`
	RevisionBefore string            `json:"revision_before"`
	RevisionAfter  string            `json:"revision_after"`
	Intents        []applyIntentJSON `json:"intents"`
	Diff           string            `json:"diff"`
	Error          *applyErrorJSON   `json:"error"`
}

const applyJSONScore = "cicada 2\ntempo 120\ntrack bass acid { cutoff = 620Hz }\ntrack lead piano {}\npattern riff { a1 . c2 . e2 . a1 . }\npattern take performance length=3840ticks {\n  note 60 at=960ticks length=480ticks velocity=90;\n}\nscene main { bass = riff lead = take }\nsong { main*2 }\n"

func runApplyJSON(t *testing.T, intents string, extra ...string) (applyJSONResult, error, string) {
	t.Helper()
	dir := t.TempDir()
	score := filepath.Join(dir, "song.cicada")
	if err := os.WriteFile(score, []byte(applyJSONScore), 0o644); err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(dir, "intents.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+intents+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := applyCommandWithHooks(append([]string{score, envelope, "--json"}, extra...), &stdout, nil)
	var result applyJSONResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("apply --json stdout is not one JSON document: %v\n%s", decodeErr, stdout.String())
	}
	if result.Schema != applySchema {
		t.Fatalf("schema %q", result.Schema)
	}
	return result, err, score
}

func TestApplyJSONReportsTheWrittenRevision(t *testing.T) {
	result, err, score := runApplyJSON(t, `{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"},{"kind":"setpatternsettings","entity":"pattern:riff","swing100":5600,"gate":60,"transpose":0}`, "--write")
	if err != nil || !result.OK || !result.Written || !result.Changed || result.Error != nil {
		t.Fatalf("apply --json --write: %v %+v", err, result)
	}
	written, readErr := os.ReadFile(score)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if result.RevisionBefore != edits.Revision([]byte(applyJSONScore)) || result.RevisionAfter != edits.Revision(written) || !strings.Contains(result.Diff, "+track bass acid { cutoff = 900Hz }") {
		t.Fatalf("revisions or diff: %+v", result)
	}
	if len(result.Intents) != 2 || result.Intents[0].Kind != "setparam" || result.Intents[1].Index != 1 || result.Intents[1].Label == "" {
		t.Fatalf("intents: %+v", result.Intents)
	}
}

func TestApplyJSONDryRunWritesNothing(t *testing.T) {
	result, err, score := runApplyJSON(t, `{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"}`)
	unchanged, _ := os.ReadFile(score)
	if err != nil || !result.OK || result.Written || !result.Changed || string(unchanged) != applyJSONScore || result.Diff == "" {
		t.Fatalf("dry run: %v %+v", err, result)
	}
}

// TestApplyErrorsNameTheFailingIntent checks the prose and the JSON of a
// decoding error, a handler error and a note refusal.
func TestApplyErrorsNameTheFailingIntent(t *testing.T) {
	for _, tc := range []struct {
		name, intents, stage, kind, field, reason, prose string
	}{
		{"decode", `{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"},{"kind":"setpatternsettings","entity":"pattern:riff","swing100":"wide"}`, "decode", "setpatternsettings", "swing100", "", "intents[1].swing100 (setpatternsettings): "},
		{"entity", `{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"},{"kind":"togglestep","entity":"step:nosuch/0"}`, "intent_refused", "togglestep", "entity", "", `intents[1].entity (togglestep): unknown pattern "nosuch"`},
		{"note", `{"kind":"editnotes","entity":"pattern:take","op":"insert","insert":[{"at":1200,"pitch":60,"length":240,"velocity":100}]}`, "intent_refused", "editnotes", "", "overlap", "intents[0] (editnotes): "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err, score := runApplyJSON(t, tc.intents, "--write")
			if err == nil || !strings.HasPrefix(err.Error(), tc.prose) {
				t.Fatalf("prose error %q, want the prefix %q", err, tc.prose)
			}
			failure := result.Error
			if result.OK || result.Written || failure == nil || failure.Code != tc.stage || failure.Index == nil || failure.Kind != tc.kind || failure.Field != tc.field || failure.Reason != tc.reason {
				t.Fatalf("JSON error: %+v", failure)
			}
			if tc.reason != "" && (failure.Diagnostic != "CICADA-NOTES" || failure.Note["pitch"] != float64(60)) {
				t.Fatalf("note refusal as data: %+v", failure)
			}
			if data, _ := os.ReadFile(score); string(data) != applyJSONScore {
				t.Fatal("a failed batch changed the score")
			}
		})
	}
}
