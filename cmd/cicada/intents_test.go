package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/testwav"
)

// catalogFixture names the fixture score in testdata/intent-catalog that an
// intent's catalog example edits. Most examples edit song.cicada.
func catalogFixture(kind string) string {
	switch kind {
	case "addmarker", "addplacement", "copyplacement", "deleteclip", "duplicateclip", "moveclip",
		"placesamplerprint", "removearrangementtempo", "removemarker", "removeplacement", "resizeclip",
		"setarrangementtempo", "setmarker", "splitclip", "updateplacement", "varyplacement":
		return "arrange.cicada"
	case "convertsongtoarrange":
		// The conversion refuses scenes with settings, which song.cicada has.
		return "convert.cicada"
	}
	return "song.cicada"
}

// TestIntentCatalogExamplesApply runs every catalog example through cicada
// apply, as a dry run, against the fixture scores in testdata/intent-catalog,
// and requires a change. A new intent must bring an example that applies.
func TestIntentCatalogExamplesApply(t *testing.T) {
	fixtures := filepath.Join("testdata", "intent-catalog")
	for _, info := range edits.Catalog() {
		t.Run(info.Kind, func(t *testing.T) {
			fixture := catalogFixture(info.Kind)
			source, err := os.ReadFile(filepath.Join(fixtures, fixture))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			score := filepath.Join(dir, "catalog.cicada")
			if err := os.WriteFile(score, source, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "audio"), 0o755); err != nil {
				t.Fatal(err)
			}
			// The examples that print or record audio name these files and
			// their SHA-256: example.wav (mono, also song.cicada's asset) and
			// print.wav (stereo, as a sampler print is).
			for name, channels := range map[string]int{"example.wav": 1, "print.wav": 2} {
				if err := os.WriteFile(filepath.Join(dir, "audio", name), testwav.Bytes(48000, channels, 16, 4800, 1), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if info.Kind == "insertlibraryitem" {
				// cicada apply does not pin libraries (Studio pins them before it
				// applies the intent), so the project pins std/synth first, as
				// the intent's notes tell a caller to.
				_, pins, err := studioLibraryPins(score, edits.LibraryImport(source, "std/synth"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pins.Path, pins.After, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			envelope := filepath.Join(dir, "intents.json")
			if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+string(info.Example)+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			if err := applyCommandWithHooks([]string{score, envelope}, &stdout, nil); err != nil {
				t.Fatalf("example %s does not apply to %s: %v", info.Example, fixture, err)
			}
			if !strings.Contains(stdout.String(), "@@") {
				t.Fatalf("example %s changes nothing in %s", info.Example, fixture)
			}
		})
	}
}

func TestIntentsCommandJSON(t *testing.T) {
	var stdout bytes.Buffer
	if err := intentsCommand([]string{"--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schema          string `json:"schema"`
		EnvelopeVersion int    `json:"envelope_version"`
		Envelope        []struct {
			Name string `json:"name"`
		} `json:"envelope"`
		Intents []struct {
			Kind    string          `json:"kind"`
			Meaning string          `json:"meaning"`
			Example json.RawMessage `json:"example"`
			Fields  []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"intents"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("intents --json is not JSON: %v", err)
	}
	if document.Schema != intentsSchema || document.EnvelopeVersion != edits.EnvelopeVersion || len(document.Envelope) == 0 {
		t.Fatalf("header: %+v", document)
	}
	if len(document.Intents) != len(edits.Kinds()) {
		t.Fatalf("catalog lists %d intents; the registry has %d", len(document.Intents), len(edits.Kinds()))
	}
	for i, intent := range document.Intents {
		if i > 0 && document.Intents[i-1].Kind >= intent.Kind {
			t.Fatalf("intents are not sorted by kind at %s", intent.Kind)
		}
		if intent.Meaning == "" || len(intent.Example) == 0 {
			t.Errorf("%s lacks a meaning or an example", intent.Kind)
		}
	}
	var one bytes.Buffer
	if err := intentsCommand([]string{"setstep"}, &one); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"setstep: ", "Fields:", "entity (entity, required)", "Example:"} {
		if !strings.Contains(one.String(), want) {
			t.Fatalf("cicada intents setstep lacks %q:\n%s", want, one.String())
		}
	}
	if err := intentsCommand([]string{"nosuchkind"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown intent kind") {
		t.Fatalf("unknown kind: %v", err)
	}
}

func TestDiagnosticsCommand(t *testing.T) {
	var stdout bytes.Buffer
	if err := diagnosticsCommand([]string{"--json"}, &stdout); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Schema      string `json:"schema"`
		Diagnostics []struct {
			Code       string   `json:"code"`
			Severities []string `json:"severities"`
			Meaning    string   `json:"meaning"`
			Fix        string   `json:"fix"`
			Reasons    []struct {
				Reason string `json:"reason"`
			} `json:"reasons"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("diagnostics --json is not JSON: %v", err)
	}
	if document.Schema != diagnosticsSchema || len(document.Diagnostics) < 70 {
		t.Fatalf("schema %q, %d codes", document.Schema, len(document.Diagnostics))
	}
	reasons := 0
	for _, entry := range document.Diagnostics {
		if entry.Meaning == "" || entry.Fix == "" || len(entry.Severities) == 0 {
			t.Errorf("%s is incomplete", entry.Code)
		}
		if entry.Code == "CICADA-NOTES" {
			reasons = len(entry.Reasons)
		}
	}
	if reasons != len(edits.NoteErrorCodes) {
		t.Fatalf("CICADA-NOTES lists %d reasons; the editor has %d", reasons, len(edits.NoteErrorCodes))
	}
	stdout.Reset()
	if err := diagnosticsCommand([]string{"slide-rest"}, &stdout); err != nil || !strings.Contains(stdout.String(), "CICADA-SLIDE-REST (warning; reported by: score)") || !strings.Contains(stdout.String(), "Fix: ") {
		t.Fatalf("diagnostics slide-rest: %v\n%s", err, stdout.String())
	}
	if err := diagnosticsCommand([]string{"CICADA-NOPE"}, &bytes.Buffer{}); err == nil {
		t.Fatal("an unknown code was accepted")
	}
}
