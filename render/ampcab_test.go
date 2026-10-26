package render

import (
	"bytes"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"testing"
)

func TestAmpCabWAVMatchesNativeAndBlockSizes(t *testing.T) {
	score, ds := notation.Parse([]byte(`cicada 2
tempo 120
fx rig ampcab { drive = 0.6 tone = 0.5 output = -3dB cabinet = 1 }
track lead acid { insert = rig }
track backing acid { level = -12dB }
pattern line acid { 1 . 3 . 5 . 3 . }
scene main { lead = line backing = line }
song { main }
`))
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
		t.Fatalf("native/export residual %g", peak)
	}
	var ref []byte
	for _, block := range []int{16, 128, 4096} {
		var out bytes.Buffer
		dither := false
		_, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .1, Block: block, Dither: &dither}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			ref = bytes.Clone(out.Bytes())
		} else if !bytes.Equal(ref, out.Bytes()) {
			t.Fatalf("different block %d", block)
		}
	}
}
