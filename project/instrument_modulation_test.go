package project

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

const graphModSource = `cicada 2
tempo 120
lfo unused { rate = 1/2 }
lfo wave { wave = square rate = 1/4 }
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * mod(wave)) } }
track voice lead {}
pattern p { c4 . }
scene main { voice = p }
song { main }
`

func TestInstrumentModulatorRoundTripAndRender(t *testing.T) {
	score, ds := notation.Parse([]byte(graphModSource))
	if score == nil || hasErrors(ds) {
		t.Fatalf("parse: %v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("project: %v", ds)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("mod(wave)")) {
		t.Fatalf("source lost modulator: %s", source)
	}
	score, ds = notation.Parse(source)
	if score == nil || hasErrors(ds) {
		t.Fatalf("reparse: %v\n%s", ds, source)
	}
	if _, ds = FromScore(score); hasErrors(ds) {
		t.Fatalf("round trip: %v", ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, n := range cfg.Track[0].Graph.Nodes[:cfg.Track[0].Graph.Len] {
		if n.Op == graph.Modulator {
			found = true
			if n.Value != 1 {
				t.Fatalf("wrong source index: %v", n.Value)
			}
		}
	}
	if !found {
		t.Fatal("graph source missing")
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Track[0].Graph != cfg.Track[0].Graph {
		t.Fatal("graph changed in image")
	}
	// The graph node record contains opcode 35 and source index 1. Mutate only
	// that index to 2, which is in the kernel's maximum range but absent from
	// this project's two-entry source table.
	needle := []byte{byte(graph.Modulator), 0, 0, 0, 0, 0, 128, 63}
	if bytes.Count(image, needle) != 1 {
		t.Fatal("could not locate unique graph source record")
	}
	bad := append([]byte(nil), image...)
	at := bytes.Index(bad, needle)
	copy(bad[at+4:at+8], []byte{0, 0, 0, 64})
	if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
		t.Fatal("image accepted a source index beyond its table")
	}
	render := func(cfg engine.Config, block int) []float32 {
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		result := make([]float32, 24000)
		left, right := make([]float32, block), make([]float32, block)
		for i := 0; i < len(result); i += block {
			e.Render(left, right)
			copy(result[i:], left)
		}
		return result
	}
	a := render(decoded, 128)
	b := render(decoded, 64)
	var first, second float64
	for i := 500; i < 10000; i++ {
		first += math.Abs(float64(a[i]))
		second += math.Abs(float64(a[i+12000]))
	}
	if first < second*2 {
		t.Fatalf("source does not modulate signal: %g vs %g", first, second)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("buffer size changed sample %d: %g != %g", i, a[i], b[i])
		}
	}
	cfg.Modulators = nil
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("image accepted missing modulator")
	}
	if _, err := engine.New(cfg); err == nil {
		t.Fatal("engine accepted missing modulator")
	}
}

func TestInstrumentModulatorRejectsUnknownAndInvalidSources(t *testing.T) {
	for _, source := range []string{strings.Replace(graphModSource, "mod(wave)", "mod(missing)", 1), strings.Replace(graphModSource, "mod(wave)", "mod(0.5)", 1), strings.Replace(graphModSource, "lfo wave { wave = square rate = 1/4 }", "envelope wave { source = voice }", 1)} {
		score, ds := notation.Parse([]byte(source))
		if score != nil && !hasErrors(ds) {
			_, ds = FromScore(score)
		}
		if !hasErrors(ds) {
			t.Fatalf("accepted invalid modulator: %s", source)
		}
	}
}
