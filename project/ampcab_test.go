package project

import (
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"testing"
)

func TestAmpCabSourceAndImageRoundTrip(t *testing.T) {
	src := `cicada 2
fx rig ampcab { drive = 0.7 tone = 0.4 output = -3dB cabinet = 1 bypass = off }
track bass acid { insert = rig }
pattern phrase acid { 1 . 3 . }
scene main { bass = phrase }
song { main }
`
	p := masterProject(t, src)
	out, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	other := masterProject(t, string(out))
	if !SemanticEqual(p, other) {
		t.Fatal("source roundtrip changed effect")
	}
	for _, rate := range []int{44100, 48000, 96000} {
		cfg, err := CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Track[0].InsertChain) != 1 || cfg.Track[0].InsertChain[0].AmpCab == nil {
			t.Fatal("missing amp stage")
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := kernelimage.Decode(image, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if *cfg.Track[0].InsertChain[0].AmpCab != *decoded.Track[0].InsertChain[0].AmpCab {
			t.Fatal("image controls changed")
		}
		e, err := engine.New(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if e.TrackLatencyFrames() != 31 {
			t.Fatalf("latency %d", e.TrackLatencyFrames())
		}
	}
}
