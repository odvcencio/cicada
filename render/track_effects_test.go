package render

import (
	"bytes"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"testing"
)

const trackEffectsRenderScore = `cicada 2
tempo 120
fx warm drive { mix = 0 }
fx edge drive { mix = 0 }
fx hp eq { type = highpass frequency = 80Hz q = 0.707 }
fx leveler comp { threshold = -18dB ratio = 3 makeup = 0dB }
fx sibilance deesser {}
fx punch transient { attack = 1dB }
master { insert = punch }
track lead acid { insert = warm -> edge -> hp -> leveler -> sibilance }
track double acid { level = -12dB }
pattern line acid { 1 . 3 . 5 . 3 . }
scene main { lead = line double = line }
song { main*2 }
`

func TestTrackEffectsWAVPreservesNativeProcessingLatencyAndBlocks(t *testing.T) {
	score, ds := notation.Parse([]byte(trackEffectsRenderScore))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	native := driftEngine(t, cfg, 1, .1, e.LatencyFrames())
	offline := driftOffline(t, score, 1, .1, 128)
	peak, _ := driftDelta(t, offline, native)
	if peak > 1e-6 {
		t.Fatalf("track effects export/native difference %g", peak)
	}
	var reference []byte
	for _, block := range []int{16, 128, 4096} {
		var out bytes.Buffer
		dither := false
		report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .1, Block: block, Dither: &dither}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if report.Frames != 100800 {
			t.Fatal("track/master latency changed exported length")
		}
		if reference == nil {
			reference = bytes.Clone(out.Bytes())
		} else if !bytes.Equal(reference, out.Bytes()) {
			t.Fatalf("export changed across block size %d", block)
		}
	}
}
