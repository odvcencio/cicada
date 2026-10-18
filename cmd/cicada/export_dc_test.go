package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/render"
)

// dcScore builds a score whose instrument graph produces real DC at the
// output (a saturated, wide pulse is asymmetric). The master level is 6 dB.
// scheduled adds a flat track EQ, which moves the render to the scheduled
// engine without removing DC. masterChain is the master insert list text.
func dcScore(t *testing.T, scheduled bool, masterFX, masterInsert string) string {
	t.Helper()
	trackInsert := ""
	if scheduled {
		trackInsert = "insert = flat"
	}
	insert := ""
	if masterInsert != "" {
		insert = "insert = " + masterInsert
	}
	text := fmt.Sprintf(`cicada 2
tempo 138
key a minor
seed 4242
instrument dcsat {
  voice mono {
    let osc = pulse(pitch, 0.9)
    let shape = env(gate, 380ms)
    out = tanh(osc * 4) * shape * 0.3
  }
}
fx flat eq { type = peak frequency = 1000Hz q = 1 gain = 0dB }
%s
track lead dcsat { %s }
pattern lead-a { 5 . . . 3 . . . | 1' . . . 7 . . . }
scene main { lead = lead-a }
song { main*4 }
master { level = 6dB %s }
export loud { loudness = -23LUFS true_peak = -1dBTP normalize = off }
`, masterFX, trackInsert, insert)
	path := filepath.Join(t.TempDir(), "dc.cicada")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runDCExport(t *testing.T, path string, scheduled bool) {
	t.Helper()
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 1}
	target := renderTargetOptions{ExportName: "loud", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	if got := render.UsesScheduledEngine(inspection.score, opts); got != scheduled {
		t.Fatalf("UsesScheduledEngine = %v, want %v", got, scheduled)
	}
	wav := filepath.Join(t.TempDir(), "dc.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	t.Logf("METRIC EXPORT-DC scheduled=%v lufs=%.3f gain=%+.2f passes=%d dc_corr=%+.6f err=%v", scheduled, result.AchievedLUFS, result.AppliedGainDB, result.Passes, result.DCCorrection.LeftFS, err)
	if err != nil {
		t.Fatalf("export failed: %v (%+v)", err, result)
	}
	if _, err := render.VerifyWAV(wav, render.VerifyOptions{SampleRate: 48000, Bits: 24, Bars: 4, TailSec: 1, PeakMaxDB: 0, DCMaxDB: -60}); err != nil {
		t.Fatalf("DC limit missed: %v", err)
	}
}

func TestExportDCConvergesWithMasterLevel(t *testing.T) {
	limiter := "fx ceiling limiter { ceiling = -1dBTP lookahead = 3ms release = 100ms }"
	for _, scheduled := range []bool{false, true} {
		for _, mode := range []struct{ name, fx, insert string }{
			{"limiter", limiter, "ceiling"},
			{"nochain", "", ""},
			{"flatchain", "", "flat"},
		} {
			t.Run(fmt.Sprintf("scheduled=%v/%s", scheduled, mode.name), func(t *testing.T) {
				runDCExport(t, dcScore(t, scheduled, mode.fx, mode.insert), scheduled)
			})
		}
	}
}

// A master highpass removes DC from the score itself. The export must still
// pass, with or without a correction.
func TestExportDCMasterHighpassPasses(t *testing.T) {
	fx := "fx tone eq { type = highpass frequency = 25Hz q = 0.707 }\nfx ceiling limiter { ceiling = -1dBTP lookahead = 3ms release = 100ms }"
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduled=%v", scheduled), func(t *testing.T) {
			runDCExport(t, dcScore(t, scheduled, fx, "tone -> ceiling"), scheduled)
		})
	}
}
