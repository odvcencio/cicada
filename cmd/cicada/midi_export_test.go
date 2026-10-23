package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/smf"
)

func TestMIDIExportReportsRecordingsAndResolvesLibraryPattern(t *testing.T) {
	// SourceFile bindings carry canonical namespaces, as the project loader does.
	aliases := map[string]string{"tone": "demo.tone"}
	source := fmt.Sprintf(`cicada 2
asset raw "recording.wav" { sha256=%q format=wav frames=48000 rate=48000Hz channels=1 }
clip pass raw { start=0frames end=48000frames }
track guitar audio {}
track bass acid {}
scene main { guitar=pass bass=tone.riff }
song { main }
`, strings.Repeat("a", 64))
	score, diagnostics := notation.ParseFiles([]notation.SourceFile{
		{Path: "main.cicada", Source: []byte(source), Bindings: aliases},
		{Path: "lib/demo/tone/main.cicada", Library: "demo/tone", Source: []byte("pattern riff { 1 . 5 . }\n")},
	}, 2)
	if hasDiagnosticErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || hasDiagnosticErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	dir := t.TempDir()
	for _, pattern := range []string{"", "tone.riff", "demo.tone.riff"} {
		output := filepath.Join(dir, fmt.Sprintf("notes-%d.mid", len(pattern)))
		report, err := os.CreateTemp(dir, "report-*")
		if err != nil {
			t.Fatal(err)
		}
		originalStderr := os.Stderr
		os.Stderr = report
		err = midiFile(p, midiOptions{output: output, pattern: pattern, report: true, libraryAliases: score.LibraryAliases})
		os.Stderr = originalStderr
		report.Close()
		if err != nil {
			t.Fatal("mixed project cannot export", pattern, err)
		}
		data, _ := os.ReadFile(report.Name())
		var status struct {
			Notes   int      `json:"notes"`
			Omitted []string `json:"audio_tracks_omitted"`
		}
		if err := json.Unmarshal(data, &status); err != nil || status.Notes != 8 {
			t.Fatal("export report lost notes", string(data), err)
		}
		if pattern == "" && !slices.Equal(status.Omitted, []string{"guitar"}) || pattern != "" && len(status.Omitted) != 0 {
			t.Fatal("omitted recordings were misreported", status)
		}
		wire, _ := os.ReadFile(output)
		file, err := smf.Decode(bytes.NewReader(wire))
		if err != nil || file.Tracks[len(file.Tracks)-1].Name != "bass" || len(file.Tracks[len(file.Tracks)-1].Notes) != 8 {
			t.Fatal("exported file lost the library pattern", err)
		}
	}
}
