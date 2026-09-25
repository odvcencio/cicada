package render

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestDriveInsertChangesBassButKeepsDryTrackAligned(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "drive-insert.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	renderSource := func(source []byte) ([]byte, Report) {
		t.Helper()
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if report.writtenFrames != report.Frames || len(output.Bytes()) != 68+int(report.Frames)*6 {
			t.Fatalf("drive changed WAV length: %+v, bytes=%d", report, output.Len())
		}
		return output.Bytes(), report
	}
	active, activeReport := renderSource(source)
	again, _ := renderSource(source)
	if !bytes.Equal(active, again) {
		t.Fatal("drive render is not deterministic")
	}
	bypassSource := bytes.Replace(source, []byte("insert = drive"), []byte("insert = none"), 1)
	bypass, bypassReport := renderSource(bypassSource)
	if activeReport.Frames != bypassReport.Frames || activeReport.OutputPeak == 0 {
		t.Fatalf("invalid active or bypass render: %+v %+v", activeReport, bypassReport)
	}
	changed := 0
	for index := 68; index+6 <= len(active); index += 6 {
		if !bytes.Equal(active[index:index+6], bypass[index:index+6]) {
			changed++
		}
	}
	if changed < 100 {
		t.Fatalf("drive changed only %d PCM frames", changed)
	}
	muted := bytes.Replace(source, []byte("cutoff = 620hz"), []byte("level = off\n  cutoff = 620hz"), 1)
	mutedActive, _ := renderSource(muted)
	mutedBypass := bytes.Replace(muted, []byte("insert = drive"), []byte("insert = none"), 1)
	mutedDry, _ := renderSource(mutedBypass)
	if !bytes.Equal(mutedActive, mutedDry) {
		t.Fatal("drive latency shifted the unprocessed drum track")
	}
}
