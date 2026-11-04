package project

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
)

func TestInstrumentGlideAndTrackOverrideRoundTrip(t *testing.T) {
	source := `cicada 2
instrument lead { glide = 125ms voice mono { out = sine(pitch) * env(gate, 100ms) } }
track first lead {}
track second lead { glide = 0.25s }
pattern p { 1~ 3 }
scene main { first = p second = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	document, err := notation.ParseDocument([]byte(source))
	if err != nil { t.Fatal(err) }
	formatted, err := notation.Format(document)
	if err != nil { t.Fatal(err) }
	if _, ds := notation.Parse(formatted); len(ds) != 0 { t.Fatalf("format changed glide: %v\n%s", ds, formatted) }
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "glide = 125ms") {
		t.Fatalf("instrument glide lost: %s", text)
	}
	cfg, err := CompileEngine(restored, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].Graph.GlideMS != 125 || cfg.Track[1].Graph.GlideMS != 250 {
		t.Fatalf("glide inheritance = %g, %g", cfg.Track[0].Graph.GlideMS, cfg.Track[1].Graph.GlideMS)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Track[0].Graph.GlideMS != 125 || decoded.Track[1].Graph.GlideMS != 250 {
		t.Fatal("glide lost in audio image")
	}
}

func TestLegacyNamedGlideParameterStaysAnInstrumentParameter(t *testing.T) {
	source := `cicada 2
instrument lead { param glide = 0.4 voice mono { out = sine(pitch) * glide * env(gate, 100ms) } }
track first lead { glide = 0.6 }
pattern p { 1~ 3 }
scene main { first = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].Graph.GlideMS != 60 {
		t.Fatalf("legacy graph glide changed to %g", cfg.Track[0].Graph.GlideMS)
	}
	found := false
	for _, node := range cfg.Track[0].Graph.Nodes[:cfg.Track[0].Graph.Len] {
		found = found || math.Abs(float64(node.Value)-0.6) < 1e-6
	}
	if !found {
		t.Fatal("legacy glide parameter override lost")
	}
}

func TestInstrumentGlideRejectsBadUnitsAndDuplicates(t *testing.T) {
	source := `cicada 2
instrument lead { glide = 100ms voice mono { out = sine(pitch) * env(gate, 100ms) } }
track first lead { glide = 50ms }
pattern p { 1 }
scene main { first = p }
song { main }
`
	for _, bad := range []string{strings.Replace(source, "glide = 100ms", "glide = 100Hz", 1), strings.Replace(source, "glide = 100ms", "glide = 100ms glide = 80ms", 1), strings.Replace(source, "glide = 50ms", "glide = -1ms", 1), strings.Replace(source, "glide = 50ms", "glide = 10001ms", 1)} {
		if _, ds := notation.Parse([]byte(bad)); len(ds) == 0 {
			t.Fatalf("accepted invalid glide: %s", bad)
		}
	}
}
