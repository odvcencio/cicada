package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRenderDriftBaselineCLI(t *testing.T) {
	dir, cli := os.Getenv("CICADA_RENDER_DRIFT_DIR"), os.Getenv("CICADA_RENDER_DRIFT_CLI")
	if dir == "" || cli == "" {
		t.Skip("set the output directory and baseline CLI built with origin/main's wav.go overlay")
	}
	for _, name := range driftExamples {
		score, _ := driftScore(t, name)
		bars := 0
		for _, entry := range score.Song {
			bars += entry.Bars
		}
		bars = min(16, bars)
		command := exec.Command(cli, "render", filepath.Join("..", "examples", name+".cicada"), "-o", filepath.Join(dir, name+".offline.wav"), "--rate", "48000", "--bits", "16", "--bars", fmt.Sprint(bars), "--tail", "3s", "--dither=false")
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("baseline CLI %s: %v: %s", name, err, data)
		}
		t.Logf("saved %s.offline.wav from the baseline CLI", name)
	}
}

func driftReadPCM16(t *testing.T, path string) driftAudio {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 68 || binary.LittleEndian.Uint16(data[34:36]) != 16 || binary.LittleEndian.Uint32(data[24:28]) != 48000 {
		t.Fatal("A/B requires stereo PCM16 at 48 kHz")
	}
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 4
	a := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range n {
		a.left[i] = float32(int16(binary.LittleEndian.Uint16(data[44+i*4:]))) / 32767
		a.right[i] = float32(int16(binary.LittleEndian.Uint16(data[46+i*4:]))) / 32767
	}
	return a
}

func TestRenderDriftControlFingerprints(t *testing.T) {
	dir := os.Getenv("CICADA_RENDER_DRIFT_DIR")
	if dir == "" {
		t.Skip("run the private-state controls first")
	}
	read := func(variant string) driftAudio {
		data, err := os.ReadFile(filepath.Join(dir, "control."+variant+".f32"))
		if err != nil {
			t.Fatal(err)
		}
		a := driftAudio{make([]float32, len(data)/8), make([]float32, len(data)/8)}
		for i := range a.left {
			a.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8:]))
			a.right[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8+4:]))
		}
		return a
	}
	reference := read("reference")
	var out bytes.Buffer
	for _, variant := range []string{"paramAlpha-immediate", "mix-immediate", "voice-budget-exact", "params-at-note-on", "reset-noise-state", "flush-output-denormals"} {
		d, peak, rms := driftMeasure(t, reference, read(variant))
		if d.MeanDB != 0 || d.MaxDB != 0 {
			t.Fatalf("%s fingerprint changed: %+v", variant, d)
		}
		fmt.Fprintf(&out, "CAUSE %s mean_db=%.9f max_db=%.1f peak=%.9g rms=%.9g\n", variant, d.MeanDB, d.MaxDB, peak, rms)
	}
	t.Log(out.String())
	if err := os.WriteFile(filepath.Join(dir, "control-fingerprints.txt"), out.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

// A is the baseline CLI export, preserved before the offline correctness fix.
// B is the engine export after the fix. Also write the corrected offline output
// so both compatibility drift and convergence are reviewable. Never play audio.
func TestRenderDriftAB(t *testing.T) {
	dir := os.Getenv("CICADA_RENDER_DRIFT_DIR")
	if dir == "" {
		t.Skip("set CICADA_RENDER_DRIFT_DIR after capturing before-change renders")
	}
	var readme, metrics bytes.Buffer
	fmt.Fprintln(&readme, "Compare the rendered pairs to assess the audible changes.")
	fmt.Fprintln(&readme, "A (*.offline.wav): cicada render from main including #92, before the chance-slot fix. B (*.unified.wav): Engine.Render after this fix, with limiter/insert latency removed.")
	fmt.Fprintln(&readme, "Additional *.corrected-offline.wav files show the fixed offline renderer. They should match B exactly. No engine production code changed; no goldens or thresholds changed.")
	fmt.Fprintln(&readme, "All listening files: stereo, 48 kHz, PCM16; first 16 bars or whole song if shorter; 3 s tail; normalization and dither off. No audio has been played.")
	fmt.Fprintln(&readme, "Fingerprint figures use float32 before PCM16 encoding and the same FingerprintStereo/FingerprintDrift code as cicada golden. The A/B peak and RMS below use the encoded PCM16 files.")
	for _, name := range driftExamples {
		score, cfg := driftScore(t, name)
		bars := 0
		for _, entry := range score.Song {
			bars += entry.Bars
		}
		bars = min(16, bars)
		beforeOffline := driftReadWAV(t, filepath.Join(dir, name+".before.offline.float.wav"))
		beforeUnified := driftReadWAV(t, filepath.Join(dir, name+".before.unified.float.wav"))
		afterOffline := driftOffline(t, score, bars, 3, 4096)
		afterUnified := driftEngine(t, cfg, bars, 3, driftLatency(t, cfg))
		before, _, _ := driftMeasure(t, beforeOffline, beforeUnified)
		after, afterPeak, _ := driftMeasure(t, afterOffline, afterUnified)
		unchanged, _ := driftDelta(t, beforeUnified, afterUnified)
		if unchanged != 0 {
			t.Fatalf("engine changed for %s by %.9g", name, unchanged)
		}
		if afterPeak != 0 {
			t.Fatalf("offline and engine still differ for %s by %.9g", name, afterPeak)
		}
		driftWriteWAV(t, filepath.Join(dir, name+".unified.wav"), afterUnified, score, bars, 16, 3)
		driftWriteWAV(t, filepath.Join(dir, name+".corrected-offline.wav"), afterOffline, score, bars, 16, 3)
		// Require the CLI-created A file; compare it against the saved float
		// render's PCM16 encoding to verify all baseline options were matched.
		baselinePath := filepath.Join(dir, name+".baseline-check.wav")
		driftWriteWAV(t, baselinePath, beforeOffline, score, bars, 16, 3)
		want, err := os.ReadFile(baselinePath)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, name+".offline.wav"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("CLI baseline differs from saved float render for %s", name)
		}
		if err := os.Remove(baselinePath); err != nil {
			t.Fatal(err)
		}
		a, b := driftReadPCM16(t, filepath.Join(dir, name+".offline.wav")), driftReadPCM16(t, filepath.Join(dir, name+".unified.wav"))
		peak, rms := driftDelta(t, a, b)
		fmt.Fprintf(&readme, "\n%s.offline.wav / %s.unified.wav: %d bars + 3 s, %.6f s (%d frames), peak difference %.9f, RMS difference %.9f\n", name, name, bars, float64(len(a.left))/48000, len(a.left), peak, rms)
		fmt.Fprintf(&metrics, "METRIC %s before_mean_db=%.9f before_max_db=%.1f after_mean_db=%.9f after_max_db=%.1f ab_pcm16_peak=%.9f ab_pcm16_rms=%.9f\n", name, before.MeanDB, before.MaxDB, after.MeanDB, after.MaxDB, peak, rms)
	}
	fmt.Fprintln(&readme, "\nBefore compares baseline offline against baseline engine. After compares corrected offline against the unchanged engine. Listen to A/B to assess the changed seeded closed-hat decisions in cicada-chorus; other pairs are identical.")
	readme.Write(metrics.Bytes())
	for name, data := range map[string][]byte{"README.txt": readme.Bytes(), "metrics.txt": metrics.Bytes()} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Log(metrics.String())
}

func TestDriftDitherDoesNotExplainChanceMismatch(t *testing.T) {
	if os.Getenv("CICADA_RENDER_DRIFT_DIR") == "" {
		t.Skip("opt-in quantization control")
	}
	score, cfg := driftScore(t, "cicada-chorus")
	native := driftEngine(t, cfg, 17, 3, driftLatency(t, cfg))
	c, commands := driftDrumCommands(t, cfg, 17, true)
	legacy := driftEngineCommands(t, c, 17, 3, driftLatency(t, c), commands)
	for _, dither := range []bool{false, true} {
		encode := func(a driftAudio) driftAudio {
			e, err := newWAVEncoder(16, dither, score.Seed, 1)
			if err != nil {
				t.Fatal(err)
			}
			out := driftAudio{make([]float32, len(a.left)), make([]float32, len(a.left))}
			var frame [4]byte
			var report Report
			for i := range a.left {
				e.writeFrame(frame[:], a.left[i], a.right[i], 1, &report)
				out.left[i] = float32(int16(binary.LittleEndian.Uint16(frame[:2]))) / 32767
				out.right[i] = float32(int16(binary.LittleEndian.Uint16(frame[2:]))) / 32767
			}
			return out
		}
		d, peak, rms := driftMeasure(t, encode(legacy), encode(native))
		t.Logf("CAUSE dither=%v mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f", dither, d.MeanDB, d.MaxDB, peak, rms)
		if peak < .15 {
			t.Fatalf("chance mismatch disappeared during PCM16 encoding: peak %.9g", peak)
		}
	}
}
