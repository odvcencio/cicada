package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func sceneTempoScore(t *testing.T) *notation.Score {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "examples", "scene-tempo.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	return score
}

// ceilFrames is the sample at which a run of ticks ends at one tempo, rounded up
// the way the kernel clock rounds.
func ceilFrames(ticks, bpmMilli int64, rate int64) int64 {
	num := ticks * 60_000 * rate
	den := bpmMilli * seq.PPQ
	return (num + den - 1) / den
}

func TestSceneTempoWAVFramesAreTheSumOfTheSegments(t *testing.T) {
	score := sceneTempoScore(t)
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000, TailSec: 1}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	// main*2 at 120, slower at 100, slowest at 84, outro*2 at 66.
	var want int64
	for _, segment := range []struct{ bars, bpm int64 }{{2, 120_000}, {1, 100_000}, {1, 84_000}, {2, 66_000}} {
		want += ceilFrames(segment.bars*seq.TicksPerBar, segment.bpm, 48_000)
	}
	want += 48_000
	if report.Frames != want || report.Bars != 6 {
		t.Fatalf("frames = %d (bars %d), want %d", report.Frames, report.Bars, want)
	}
	data := wav.Bytes()
	if got := int64(binary.LittleEndian.Uint32(data[40:44])) / 6; got != want {
		t.Fatalf("data chunk holds %d frames, want %d", got, want)
	}
	// Version 2 chunk: score tempo, bars, tail, then three (bar, tempo) steps.
	chunk := data[44+want*6:]
	if string(chunk[:4]) != "cica" || binary.LittleEndian.Uint32(chunk[4:8]) != 16+3*8 || binary.LittleEndian.Uint32(chunk[8:12]) != 2 {
		t.Fatalf("timing chunk header: % x", chunk[:12])
	}
	for i, step := range [][2]uint32{{2, 100_000}, {3, 84_000}, {4, 66_000}} {
		if binary.LittleEndian.Uint32(chunk[24+8*i:]) != step[0] || binary.LittleEndian.Uint32(chunk[28+8*i:]) != step[1] {
			t.Fatalf("tempo step %d: % x", i, chunk[24+8*i:32+8*i])
		}
	}
	if int64(binary.LittleEndian.Uint32(data[4:8])) != int64(len(data))-8 {
		t.Fatal("RIFF size does not cover the version 2 chunk")
	}
	path := filepath.Join(t.TempDir(), "scene-tempo.wav")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 6, TailSec: 1, PeakMaxDB: 0, DCMaxDB: 0})
	if err != nil || verified.Frames != want {
		t.Fatalf("verify-wav: %+v, %v", verified, err)
	}
	// Dropping a tempo step makes the length wrong, so verification fails.
	broken := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(broken[44+want*6+28:], 120_000)
	if err := os.WriteFile(path, broken, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 6, TailSec: 1, PeakMaxDB: 0, DCMaxDB: 0}); err == nil {
		t.Fatal("verify-wav accepted a tempo step that does not match the frame count")
	}
}

func TestVersion1TimingChunkStillVerifies(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(testScore))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: .25}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	chunk := data[44+report.Frames*6:]
	if len(chunk) != 24 || binary.LittleEndian.Uint32(chunk[4:8]) != 16 || binary.LittleEndian.Uint32(chunk[8:12]) != 1 {
		t.Fatalf("a song with one tempo must keep the version 1 chunk: % x", chunk)
	}
	path := filepath.Join(t.TempDir(), "old.wav")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 1, TailSec: .25, PeakMaxDB: 0, DCMaxDB: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestSceneTempoStemsVerify(t *testing.T) {
	score := sceneTempoScore(t)
	dir := filepath.Join(t.TempDir(), "stems")
	noDither := false
	report, err := Stems(score, Options{SampleRate: 48_000, TailSec: .5, Dither: &noDither}, dir)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80})
	if err != nil || verified.Frames != report.Frames {
		t.Fatalf("verify-stems: %+v, %v", verified, err)
	}
}

func TestSceneTempoBlockSizeDoesNotChangeTheRender(t *testing.T) {
	score := sceneTempoScore(t)
	noDither := false
	var first, second bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, TailSec: .5, Block: 64, Dither: &noDither}, &first); err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48_000, TailSec: .5, Block: 128, Dither: &noDither}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("offline render depends on block size across tempo changes")
	}
}

// envelope sums the energy of each window.
func envelope(a driftAudio, window int) []float64 {
	out := make([]float64, len(a.left)/window)
	for i := range out {
		for _, v := range a.left[i*window : (i+1)*window] {
			out[i] += float64(v) * float64(v)
		}
	}
	return out
}

func correlation(a, b []float64) float64 {
	var sa, sb, saa, sbb, sab float64
	n := float64(len(a))
	for i := range a {
		sa, sb, saa, sbb, sab = sa+a[i], sb+b[i], saa+a[i]*a[i], sbb+b[i]*b[i], sab+a[i]*b[i]
	}
	return (sab - sa*sb/n) / math.Sqrt((saa-sa*sa/n)*(sbb-sb*sb/n))
}

// The offline renderer and the kernel place notes on the same samples across
// the tempo steps: their energy envelopes follow each other.
func TestSceneTempoOfflineMatchesKernelTiming(t *testing.T) {
	score := sceneTempoScore(t)
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatal(diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	noDither := false
	var wav bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, TailSec: 0, Dither: &noDither}, &wav); err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	offline := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range offline.left {
		offline.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
	}
	tempo, err := project.NewTempoMap(48_000, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	if int64(n) != tempo.SampleAtTick(6*seq.TicksPerBar) {
		t.Fatalf("offline length %d, tempo map %d", n, tempo.SampleAtTick(6*seq.TicksPerBar))
	}
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	native := driftAudio{make([]float32, n), make([]float32, n)}
	for at := 0; at < n; {
		frames := min(128, n-at)
		player.Render(native.left[at:at+frames], native.right[at:at+frames])
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault %d", message.A)
			}
		}
		at += frames
	}
	// Window of 20 ms; the limiter's latency shifts the offline file by a few ms.
	if c := correlation(envelope(offline, 960), envelope(native, 960)); c < .9 {
		t.Fatalf("offline and kernel energy envelopes correlate at %.3f", c)
	}
}
