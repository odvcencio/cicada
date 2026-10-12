package project

import (
	"bytes"
	"encoding/binary"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"testing"
)

const vocalChainScore = `cicada 2
fx hp eq { type = highpass frequency = 80Hz q = 0.707 }
fx body eq { type = peak frequency = 350Hz q = 1 gain = -2dB }
fx vocal_comp comp { threshold = -18dB ratio = 3 makeup = 0dB }
fx deess deesser { frequency = 6000Hz threshold = -24dB ratio = 4 range = 9dB }
fx glue comp { threshold = -12dB ratio = 2 makeup = 0dB }
bus music { insert = glue }
track lead acid { insert = hp -> body -> vocal_comp -> deess }
track double acid { insert = hp -> vocal_comp -> deess }
pattern phrase acid { 1 . 3 . 5 . 3 . }
scene main { lead = phrase double = phrase }
song { main*2 }
`

func TestTrackChainSourceJSONAndImagePreserveRender(t *testing.T) {
	p := masterProject(t, vocalChainScore)
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	p2 := masterProject(t, string(source))
	if !SemanticEqual(p, p2) {
		t.Fatal("source lost track stages")
	}
	js, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	p2, err = DecodeJSON(js)
	if err != nil || !SemanticEqual(p, p2) {
		t.Fatalf("JSON lost track stages %v", err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		cfg, err := CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CompMusic == nil || cfg.CompMusic.Threshold != -12 || len(cfg.Track[0].InsertChain) != 4 || len(cfg.Track[1].InsertChain) != 3 {
			t.Fatal("track compression leaked to music bus")
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(image[30:32])&kernelimage.TrackEffectsCapability == 0 {
			t.Fatal("missing track effect capability")
		}
		decoded, err := kernelimage.Decode(image, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		a, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		b, err := engine.New(decoded)
		if err != nil {
			t.Fatal(err)
		}
		a.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		b.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		var al, ar, bl, br [128]float32
		for i := 0; i < 1000; i++ {
			a.Render(al[:], ar[:])
			b.Render(bl[:], br[:])
			if al != bl || ar != br {
				t.Fatalf("native/image render differed at rate %d block %d", rate, i)
			}
		}
		bits := binary.LittleEndian.Uint16(image[30:32])
		binary.LittleEndian.PutUint16(image[30:32], bits&^kernelimage.TrackEffectsCapability)
		if _, err := kernelimage.Decode(image, rate, 128); err == nil {
			t.Fatal("chain accepted without capability")
		}
	}
	if !bytes.Contains(source, []byte("hp -> body -> vocal_comp -> deess")) {
		t.Fatal("authored chain order missing")
	}
}
func TestTrackChainRejectsInvalidRoutesAndValues(t *testing.T) {
	for _, mutation := range []struct{ from, to string }{
		{"insert = hp -> body -> vocal_comp -> deess", "insert = hp -> hp"},
		{"frequency = 6000Hz", "frequency = 16000Hz"},
		{"range = 9dB", "range = 30dB"},
		{"makeup = 0dB }\nfx deess", "makeup = 0dB sidechain = double }\nfx deess"},
	} {
		source := bytes.Replace([]byte(vocalChainScore), []byte(mutation.from), []byte(mutation.to), 1)
		score, ds := notation.Parse(source)
		if hasTestErrors(ds) {
			continue
		}
		p, ds := FromScore(score)
		if p != nil && !hasTestErrors(ds) {
			t.Fatalf("accepted invalid chain %s", mutation.to)
		}
	}
	p := masterProject(t, vocalChainScore)
	if _, err := ResolveParameterPath(p, "vocal_comp.threshold"); err == nil {
		t.Fatal("track comp incorrectly advertised global live automation")
	}
}
func hasTestErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
