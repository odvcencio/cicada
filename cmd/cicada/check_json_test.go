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

// checkJSONDocument mirrors the fields of cicada.check.v1 the tests read.
type checkJSONDocument struct {
	Schema   string `json:"schema"`
	Profile  string `json:"profile"`
	Valid    bool   `json:"valid"`
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Scores   []struct {
		File        string `json:"file"`
		Revision    string `json:"revision"`
		Valid       bool   `json:"valid"`
		Diagnostics []struct {
			Code     string         `json:"code"`
			Severity string         `json:"severity"`
			Message  string         `json:"message"`
			File     string         `json:"file"`
			Start    *checkPosition `json:"start"`
			End      *checkPosition `json:"end"`
			Hint     string         `json:"hint"`
			Fix      *struct {
				Summary  string            `json:"summary"`
				Intents  []json.RawMessage `json:"intents"`
				Command  []string          `json:"command"`
				Revision string            `json:"revision"`
			} `json:"fix"`
		} `json:"diagnostics"`
		Browser *struct {
			Tracks []struct {
				ID     string `json:"id"`
				Voices int    `json:"voices"`
			} `json:"tracks"`
			Voices    int `json:"voices"`
			MaxVoices int `json:"max_voices"`
		} `json:"browser_profile"`
	} `json:"scores"`
}

func runCheckJSON(t *testing.T, args ...string) (checkJSONDocument, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := checkCommand(append([]string{"--json"}, args...), &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("check --json wrote to stderr:\n%s", stderr.String())
	}
	var document checkJSONDocument
	if decodeErr := json.Unmarshal(stdout.Bytes(), &document); decodeErr != nil {
		t.Fatalf("check --json stdout is not one JSON document: %v\n%s", decodeErr, stdout.String())
	}
	if document.Schema != checkSchema {
		t.Fatalf("schema %q", document.Schema)
	}
	return document, err
}

func TestCheckJSONReportsDiagnosticsWithPositions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.cicada")
	source := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . zz }\nscene main { bass = riff }\nsong { main*2 }\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := runCheckJSON(t, path)
	if err == nil || err.Error() != "1 error(s) across 1 score(s)" {
		t.Fatalf("check --json must fail as check does: %v", err)
	}
	if document.Valid || document.Errors != 1 || len(document.Scores) != 1 || document.Scores[0].Valid {
		t.Fatalf("summary: %+v", document)
	}
	diagnostic := document.Scores[0].Diagnostics[0]
	if diagnostic.Code != "CICADA-SYNTAX" || diagnostic.Severity != "error" || diagnostic.File != path || diagnostic.Start == nil || diagnostic.End == nil {
		t.Fatalf("diagnostic: %+v", diagnostic)
	}
	line4 := strings.Index(source, "pattern riff")
	if *diagnostic.Start != (checkPosition{Line: 4, Column: 1, Offset: line4}) || *diagnostic.End != (checkPosition{Line: 4, Column: 2, Offset: line4 + 1}) {
		t.Fatalf("positions: start %+v end %+v", *diagnostic.Start, *diagnostic.End)
	}
}

// TestCheckJSONOffsetsCountBytes keeps offsets in bytes and columns in
// characters on a line with multi-byte text before the reported column.
func TestCheckJSONOffsetsCountBytes(t *testing.T) {
	source := []byte("title \"é→\" x\n")
	at := sourcePosition(source, 1, 12)
	if at.Offset != bytes.Index(source, []byte("x")) {
		t.Fatalf("offset %d, want %d", at.Offset, bytes.Index(source, []byte("x")))
	}
}

func TestCheckJSONOffersAnIntentFixThatApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.cicada")
	source := "cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { a1 . . . e2 . . . }\narrange {\n  place intro bass pulse { at = 100ticks length = 1bars }\n}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := runCheckJSON(t, path)
	if err == nil {
		t.Fatal("an off-step placement must fail the check")
	}
	diagnostic := document.Scores[0].Diagnostics[0]
	if diagnostic.Code != "CICADA-ARRANGEMENT" || diagnostic.Fix == nil || len(diagnostic.Fix.Intents) != 1 {
		t.Fatalf("diagnostic has no intent fix: %+v", diagnostic)
	}
	envelope := filepath.Join(dir, "fix.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+string(diagnostic.Fix.Intents[0])+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyCommandWithHooks([]string{path, envelope, "--write"}, &bytes.Buffer{}, nil); err != nil {
		t.Fatalf("the fix does not apply: %v", err)
	}
	if document, err := runCheckJSON(t, path); err != nil || !document.Valid {
		t.Fatalf("score is still invalid after the fix: %v %+v", err, document)
	}
}

// TestCheckJSONReportsEachScoresRevision gives every score its revision, valid
// or not, in the form cicada apply checks, and equal to the revision of a fix.
func TestCheckJSONReportsEachScoresRevision(t *testing.T) {
	dir := t.TempDir()
	scores := map[string]string{
		"valid.cicada":  "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n",
		"syntax.cicada": "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . zz }\nscene main { bass = riff }\nsong { main*2 }\n",
		"snap.cicada":   "cicada 2\ntempo 120\ntrack bass acid {}\npattern pulse { a1 . . . e2 . . . }\narrange {\n  place intro bass pulse { at = 100ticks length = 1bars }\n}\n",
	}
	for name, source := range scores {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		document, _ := runCheckJSON(t, path)
		if len(document.Scores) != 1 || document.Scores[0].Revision != edits.Revision([]byte(source)) {
			t.Fatalf("%s: scores %+v, want revision %s", name, document.Scores, edits.Revision([]byte(source)))
		}
		for _, diagnostic := range document.Scores[0].Diagnostics {
			if diagnostic.Fix != nil && diagnostic.Fix.Revision != document.Scores[0].Revision {
				t.Fatalf("%s: fix revision %s, score revision %s", name, diagnostic.Fix.Revision, document.Scores[0].Revision)
			}
		}
	}
	// The revision is what apply compares: an envelope that carries it
	// applies, one that carries another is refused.
	path := filepath.Join(dir, "valid.cicada")
	document, err := runCheckJSON(t, path)
	if err != nil {
		t.Fatal(err)
	}
	envelope := filepath.Join(dir, "edit.json")
	for _, tc := range []struct {
		revision string
		ok       bool
	}{{strings.Repeat("0", 64), false}, {document.Scores[0].Revision, true}} {
		if err := os.WriteFile(envelope, []byte(`{"version":1,"revision":"`+tc.revision+`","intents":[{"kind":"setparam","entity":"param:bass.cutoff","value":"900Hz"}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := applyCommandWithHooks([]string{path, envelope}, &bytes.Buffer{}, nil); (err == nil) != tc.ok {
			t.Fatalf("apply at revision %s: %v", tc.revision, err)
		}
	}
	// A score that cannot be read has no revision.
	document, _ = runCheckJSON(t, filepath.Join(dir, "missing.cicada"))
	if len(document.Scores) != 1 || document.Scores[0].Revision != "" {
		t.Fatalf("missing score: %+v", document.Scores)
	}
}

// TestCheckJSONAgreesWithText runs the text and JSON checks on the same
// scores and requires the same result and the same count of errors.
func TestCheckJSONAgreesWithText(t *testing.T) {
	dir := t.TempDir()
	scores := map[string]string{
		"valid.cicada":   "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n",
		"warning.cicada": "cicada 2\ntempo 120\ntrack bass acid { solo = on }\npattern p { a1 a1~ . . }\nscene main { bass = p }\nsong { main }\n",
		"errors.cicada":  "cicada 2\ntempo 120\ntrack bass acid { cutoff = 9 }\npattern riff { a1 . c2 . }\nscene main { bass = riff drums = beat }\nsong { main*2 }\n",
	}
	for name, source := range scores {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name := range scores {
		for _, profile := range []string{profileDesktop, profileBrowser} {
			path := filepath.Join(dir, name)
			var stdout, stderr bytes.Buffer
			textErr := checkCommand([]string{"--profile", profile, path}, &stdout, &stderr)
			document, jsonErr := runCheckJSON(t, "--profile", profile, path)
			if (textErr == nil) != (jsonErr == nil) || textErr != nil && textErr.Error() != jsonErr.Error() {
				t.Fatalf("%s %s: text %v, JSON %v", name, profile, textErr, jsonErr)
			}
			if document.Valid != (textErr == nil) || document.Profile != profile {
				t.Fatalf("%s %s: valid %t, text error %v", name, profile, document.Valid, textErr)
			}
			if profile == profileBrowser && textErr == nil && (document.Scores[0].Browser == nil || len(document.Scores[0].Browser.Tracks) == 0 || document.Scores[0].Browser.MaxVoices == 0) {
				t.Fatalf("%s: browser profile missing: %+v", name, document.Scores[0])
			}
			warnings := strings.Count(stderr.String(), ": warning ")
			if document.Warnings != warnings {
				t.Fatalf("%s %s: JSON has %d warnings, text %d:\n%s", name, profile, document.Warnings, warnings, stderr.String())
			}
		}
	}
}
