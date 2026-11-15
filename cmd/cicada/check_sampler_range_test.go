package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/render"
)

// writeRangeProject writes the reviewer's reproduction, a one-asset sampler
// with root c3 whose pattern played MIDI 119, with pattern as the pattern
// line, and returns the score's path and text.
func writeRangeProject(t *testing.T, pattern string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	wav := testwav.Bytes(48000, 1, 16, 4800, 1)
	if err := os.MkdirAll(filepath.Join(dir, "audio"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audio", "example.wav"), wav, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cicada.mod"), []byte("project ratio\ncicada 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`tempo 120
asset vocal "audio/example.wav" {
  sha256 = "%x"
  format = wav
  frames = 4800
  rate = 48000Hz
  channels = 1
  source = generated
}
sampler hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops hit { level = -8dB }
%s
scene verse { chops = hits }
song { verse*2 }
`, sha256.Sum256(wav), pattern)
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, source
}

// The review's reproduction: check called the score valid and render exited 1
// with "sample playback ratio must be between 0.125 and 8". Check now refuses
// it at the note, with the range the sampler plays and the note it found.
func TestCheckRefusesASamplerNoteRenderCannotPlay(t *testing.T) {
	path, source := writeRangeProject(t, "pattern hits { a2 . b6'' . }")
	line, column := positionOf(t, source, "pattern hits", "b6''")

	var stdout, stderr bytes.Buffer
	err := checkPaths([]string{path}, &stdout, &stderr)
	if err == nil || err.Error() != "1 error(s) across 1 score(s)" || stdout.Len() != 0 {
		t.Fatalf("check: %v\nstdout %q\nstderr %s", err, stdout.String(), stderr.String())
	}
	want := fmt.Sprintf("%s:%d:%d: error CICADA-SAMPLER-RANGE: pitch b6'' (MIDI 119) on track chops, pattern hits, is out of range for sampler hit: with root c3 (MIDI 48), its 48000 Hz asset vocal plays MIDI 24 to 82 (c1 to a#5) at 44.1, 48 and 96 kHz; this note needs playback ratio 65.8 at 44.1 kHz, 60.4 at 48 kHz, 30.2 at 96 kHz, and the sample voice plays 0.125 to 8", path, line, column)
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("check text\n%s\nwant a line\n%s", stderr.String(), want)
	}
	if !strings.Contains(stderr.String(), "set the sampler's root nearer the note") {
		t.Fatalf("check text gives no fix:\n%s", stderr.String())
	}

	stdout.Reset()
	if err := checkPathsJSON([]string{path}, checkOptions{profile: profileDesktop}, &stdout); err == nil {
		t.Fatal("check --json accepted the score")
	}
	var document checkDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Valid || document.Errors != 1 || len(document.Scores) != 1 || len(document.Scores[0].Diagnostics) != 1 {
		t.Fatalf("check --json: %s", stdout.String())
	}
	d := document.Scores[0].Diagnostics[0]
	if d.Code != "CICADA-SAMPLER-RANGE" || d.Severity != "error" || d.Start == nil || d.Start.Line != line || d.Start.Column != column ||
		d.Expected != "MIDI 24 to 82" || d.Found != "MIDI 119" || d.Hint == "" || !strings.HasPrefix(d.Message, "pitch b6'' (MIDI 119) on track chops") {
		t.Fatalf("diagnostic %+v", d)
	}

	// The same score renders once the note is in range.
	path, _ = writeRangeProject(t, "pattern hits { a2 . b5 . }")
	stdout.Reset()
	stderr.Reset()
	if err := checkPaths([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("check of b5: %v\n%s", err, stderr.String())
	}
}

// Check and render agree at the edges of a sampler's range. From a 48 kHz
// asset with root c3 (MIDI 48), MIDI 23 plays at 44.1 and 48 kHz but not at
// 96 kHz, and MIDI 83 at 48 and 96 kHz but not at 44.1 kHz: check warns and
// names that rate, and render fails there alone. MIDI 11 and 85 fail at
// 48 kHz, where render renders by default: check refuses them.
func TestCheckAndRenderAgreeAtTheEdgesOfASamplersRange(t *testing.T) {
	for _, tc := range []struct {
		pitch    string
		midi     int
		fails    []int
		severity string
	}{
		{"b0,", 11, []int{48_000, 96_000}, "error"},
		{"b0", 23, []int{96_000}, "warning"},
		{"c1", 24, nil, ""},
		{"a#5", 82, nil, ""},
		{"b5", 83, []int{44_100}, "warning"},
		{"c#6", 85, []int{44_100, 48_000}, "error"},
	} {
		t.Run(tc.pitch, func(t *testing.T) {
			path, _ := writeRangeProject(t, "pattern hits { "+tc.pitch+" . }")
			report := checkScoreJSON(path, checkOptions{profile: profileDesktop})
			if report.Valid != (tc.severity != "error") {
				t.Fatalf("check valid %v, want %v: %+v", report.Valid, tc.severity != "error", report.Diagnostics)
			}
			if tc.severity == "" {
				if len(report.Diagnostics) != 0 {
					t.Fatalf("check reported %+v", report.Diagnostics)
				}
			} else {
				if len(report.Diagnostics) != 1 {
					t.Fatalf("check reported %+v", report.Diagnostics)
				}
				d := report.Diagnostics[0]
				var named []string
				for _, rate := range tc.fails {
					named = append(named, fmt.Sprintf("%.3g at %g kHz", sampleasset.PlaybackRatio(48_000, rate, 48, tc.midi), float64(rate)/1000))
				}
				says := "this note needs playback ratio " + strings.Join(named, ", ") + ", and the sample voice"
				if d.Code != "CICADA-SAMPLER-RANGE" || d.Severity != tc.severity || d.Found != fmt.Sprintf("MIDI %d", tc.midi) || d.Expected != "MIDI 24 to 82" || !strings.Contains(d.Message, says) {
					t.Fatalf("diagnostic %+v; want severity %s and %q", d, tc.severity, says)
				}
			}
			inspection, err := inspectScore(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, rate := range sampleasset.OutputRates {
				opts := render.Options{SampleRate: rate, Bits: 24, AssetRoot: inspection.root(path), AssetDir: inspection.root(path)}
				_, err := render.WAV(inspection.score, opts, io.Discard)
				if refused := slices.Contains(tc.fails, rate); (err != nil) != refused {
					t.Errorf("render at %d Hz: %v; check said it fails there: %v", rate, err, refused)
				}
			}
		})
	}
}

// An edit that would write such a note is refused with the same code, and the
// range and the note as data.
func TestApplyRefusesAnEditThatPutsASamplerNoteOutOfRange(t *testing.T) {
	path, source := writeRangeProject(t, "pattern hits { a2 . c3 . }")
	envelope := filepath.Join(filepath.Dir(path), "intents.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[{"kind":"setpitch","entity":"step:hits/2","pitch":119}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := applyCommandWithHooks([]string{path, envelope, "--json", "--write"}, &stdout, nil)
	var result applyJSONResult
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("%v\n%s", decodeErr, stdout.String())
	}
	if err == nil || result.OK || result.Written || result.Error == nil || result.Error.Code != "invalid_result" ||
		result.Error.Diagnostic != "CICADA-SAMPLER-RANGE" || result.Error.Expected != "MIDI 24 to 82" || result.Error.Found != "MIDI 119" ||
		!strings.Contains(result.Error.Message, "CICADA-SAMPLER-RANGE: pitch") {
		t.Fatalf("apply: %v\n%s", err, stdout.String())
	}
	if after, _ := os.ReadFile(path); string(after) != source {
		t.Fatal("the refused edit was written")
	}

	// The same edit to a note in range goes through.
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[{"kind":"setpitch","entity":"step:hits/2","pitch":72}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := applyCommandWithHooks([]string{path, envelope, "--json"}, &stdout, nil); err != nil {
		t.Fatalf("apply of MIDI 72: %v\n%s", err, stdout.String())
	}
}
