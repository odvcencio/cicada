package render

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestCompressorBusChangesWAVDeterministically(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "compressor-bus.cicada"))
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
			t.Fatalf("compressor changed WAV duration: %+v, bytes=%d", report, output.Len())
		}
		return output.Bytes(), report
	}
	compressed, report := render(source)
	again, _ := render(source)
	if !bytes.Equal(compressed, again) {
		t.Fatal("compressor render is not deterministic")
	}
	bypass := bytes.Replace(source, []byte("ratio = 4"), []byte("ratio = 1"), 1)
	dry, _ := render(bypass)
	changed := 0
	for index := 68; index+6 <= len(compressed); index += 6 {
		if !bytes.Equal(compressed[index:index+6], dry[index:index+6]) {
			changed++
		}
	}
	if changed < 1_000 || report.OutputPeak == 0 || report.ClippedSamples != 0 {
		t.Fatalf("compressor was inaudible or clipped: changed=%d report=%+v", changed, report)
	}
}
