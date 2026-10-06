package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const masterScore = `cicada 2
tempo 120
track bass acid { level = 6dB }
pattern pulse acid { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*2 }
fx tone eq { type = highpass frequency = 80Hz }
fx glue comp { threshold = -40dB ratio = 3 makeup = 0dB }
fx punch transient { attack = 3dB sustain = -2dB }
fx stereo width { amount = 0.8 }
fx ceiling limiter { ceiling = -1dBTP }
master { insert = tone -> glue -> punch -> stereo -> ceiling }
`

func TestMasterRenderHonorsOrderLatencyAndBlockSize(t *testing.T) {
	score, ds := notation.Parse([]byte(masterScore))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	// Native and offline paths share prepared controls and master placement.
	offline := masterOffline(t, score)
	trim := driftLatency(t, cfg) + cfg.MasterProcessor.LatencyFrames()
	native := driftEngine(t, cfg, 1, .1, trim)
	peak, _ := driftDelta(t, offline, native)
	t.Logf("METRIC MASTER native_offline_peak_residual=%g", peak)
	if peak > 1e-6 {
		t.Fatalf("native/offline master residual: %g", peak)
	}
	var reference []byte
	for _, block := range []int{16, 128, 4096} {
		var out bytes.Buffer
		dither := false
		report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, Block: block, Dither: &dither}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if report.Frames != 96000 || int64(out.Len()) != 68+report.Frames*8 {
			t.Fatalf("latency changed duration: %+v bytes=%d", report, out.Len())
		}
		if reference != nil && !bytes.Equal(reference, out.Bytes()) {
			t.Fatalf("block %d changed master audio", block)
		}
		reference = append([]byte(nil), out.Bytes()...)
	}
	reversed, ds := notation.Parse([]byte(strings.Replace(masterScore, "tone -> glue -> punch -> stereo -> ceiling", "glue -> tone -> punch -> stereo -> ceiling", 1)))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	var out bytes.Buffer
	_, err = WAV(reversed, Options{SampleRate: 48000, Bits: 32, Bars: 1}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(reference, out.Bytes()) {
		t.Fatal("noncommuting master stages ignored source order")
	}
}

func masterOffline(t *testing.T, score *notation.Score) driftAudio {
	t.Helper()
	var out bytes.Buffer
	_, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .1, Block: 128}, &out)
	if err != nil {
		t.Fatal(err)
	}
	header, err := audioasset.ReadWAVHeader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	pcm := out.Bytes()[header.DataOffset:]
	audio := driftAudio{make([]float32, header.Frames), make([]float32, header.Frames)}
	for i := range audio.left {
		audio.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8:]))
		audio.right[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8+4:]))
	}
	return audio
}

func TestMasterConvolutionRenderAlignmentAndDryBypass(t *testing.T) {
	dir := t.TempDir()
	wav := testwav.Bytes(48000, 1, 16, 32, 1)
	if err := os.WriteFile(filepath.Join(dir, "impulse.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	source := `cicada 2
tempo 120
track bass acid { level = 0dB }
pattern pulse acid { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*2 }
master { insert = room }
`
	source += fmt.Sprintf(`asset room-ir "impulse.wav" { sha256 = "%x" format = wav frames = 32 rate = 48000Hz channels = 1 }
fx room convolution { asset = room-ir mix = 0 partition = 512frames }
`, sha256.Sum256(wav))
	path := filepath.Join(dir, "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	score, ds, err := project.LoadScore(path, nil)
	if err != nil || len(ds) != 0 {
		t.Fatalf("load impulse score: %v %+v", err, ds)
	}
	var dry bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, Block: 16}, &dry); err != nil {
		t.Fatal(err)
	}
	bypass, ds := notation.Parse([]byte(strings.Replace(source, "insert = room", "insert = none", 1)))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	// Remove the unused declaration and asset from the bypass comparison.
	bypass.Effects, bypass.Assets = nil, nil
	var reference bytes.Buffer
	if _, err := WAV(bypass, Options{SampleRate: 48000, Bits: 32, Bars: 1, Block: 128}, &reference); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dry.Bytes(), reference.Bytes()) {
		t.Fatal("dry convolution mix changed alignment or duration")
	}
	for i := range score.Effects[0].Params {
		if score.Effects[0].Params[i].Name == "mix" {
			score.Effects[0].Params[i].Value = "0.2"
		}
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	wet := masterOffline(t, score)
	native := driftEngine(t, cfg, 1, .1, driftLatency(t, cfg)+cfg.MasterProcessor.LatencyFrames())
	peak, _ := driftDelta(t, wet, native)
	if peak > 1e-6 {
		t.Fatalf("convolution native/offline residual: %g", peak)
	}
}
