package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/kernel/loudness"
	"m31labs.dev/cicada/notation"
)

const (
	keysLevelTarget    = -24.0
	keysLevelTolerance = 0.5
)

// keysReferenceLUFS renders the first four bars of examples/keys/<patch>.cicada
// at 0 dB and returns the BS.1770 integrated loudness of the result.
func keysReferenceLUFS(t *testing.T, patch string) float64 {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "examples", "keys", patch+".cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	var wav bytes.Buffer
	dither := false
	report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 4, Dither: &dither}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("METRIC KEYS patch=%s peak=%.3f output_peak=%.3f ceiling_samples=%d", patch, report.Peak, report.OutputPeak, report.CeilingSamples)
	data := wav.Bytes()
	frames := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	left, right := make([]float32, frames), make([]float32, frames)
	for i := range left {
		left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
		right[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[48+i*8:]))
	}
	meter, err := loudness.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := meter.ProcessBlock(left, right); err != nil {
		t.Fatal(err)
	}
	if err := meter.Finish(); err != nil {
		t.Fatal(err)
	}
	return meter.Metrics().IntegratedLUFS
}

// TestKeysReferenceLevels checks that every modeled keyboard patch reads
// -24 LUFS (+/- 0.5 LU) in its examples/keys reference over four bars at 0 dB.
func TestKeysReferenceLevels(t *testing.T) {
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			got := keysReferenceLUFS(t, name)
			trim := keyboard.LevelTrimDB[keyboard.ID(name)-1]
			t.Logf("METRIC KEYS patch=%s trim_db=%.2f integrated_lufs=%.3f", name, trim, got)
			if math.Abs(got-keysLevelTarget) > keysLevelTolerance {
				t.Fatalf("%s: %.2f LUFS, want %.1f +/- %.1f (trim %.2f dB; suggested trim %.2f dB)", name, got, keysLevelTarget, keysLevelTolerance, trim, float64(trim)+keysLevelTarget-got)
			}
		})
	}
}
