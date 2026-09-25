package render

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestReverbSendChangesWAVDeterministically(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	render := func(source []byte) ([]byte, Report) {
		t.Helper()
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: 1}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if report.writtenFrames != report.Frames || len(output.Bytes()) != 68+int(report.Frames)*6 {
			t.Fatalf("reverb changed WAV duration: %+v, bytes=%d", report, output.Len())
		}
		return output.Bytes(), report
	}
	wet, report := render(source)
	again, _ := render(source)
	if !bytes.Equal(wet, again) {
		t.Fatal("reverb render is not deterministic")
	}
	drySource := bytes.Replace(source, []byte("send_b = 0.35"), []byte("send_b = 0"), 1)
	drySource = bytes.Replace(drySource, []byte("send_b = 0.2"), []byte("send_b = 0"), 1)
	dry, _ := render(drySource)
	changed := 0
	for index := 68; index+6 <= len(wet); index += 6 {
		if !bytes.Equal(wet[index:index+6], dry[index:index+6]) {
			changed++
		}
	}
	if changed < 1_000 || report.OutputPeak == 0 || report.ClippedSamples != 0 {
		t.Fatalf("reverb return was silent or clipped: changed=%d report=%+v", changed, report)
	}
}
