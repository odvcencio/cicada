package project

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
)

func TestMappedMacroSourceJSONAndImage(t *testing.T) {
	source := []byte(`cicada 2
track bass acid {}
live { macro filter -> bass.cutoff 200Hz..4kHz curve exp smooth 150ms }
pattern p { 1 . }
scene main { bass = p }
song { main }
`)
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if len(p.Live.Macros) != 1 || p.Live.Macros[0].Mapping == nil {
		t.Fatalf("mapping lost: %+v", p.Live)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	surface := LiveSurfaceOf(restored)
	*surface.Macros[0].Mapping.Min.Number = 999
	if *restored.Live.Macros[0].Mapping.Min.Number != 200 {
		t.Fatal("host surface shares mutable mapping values")
	}
	text, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(text, []byte("macro filter -> bass.cutoff 200Hz..4000Hz curve exp smooth 150ms")) {
		t.Fatalf("mapping lost in source: %s", text)
	}
	cfg, err := CompileEngine(restored, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.MacroMappings) != 1 || !decoded.MacroMappings[0].Exponential || decoded.MacroMappings[0].Min != 200 || decoded.MacroMappings[0].Max != 4000 {
		t.Fatalf("mapping lost in image: %+v", decoded.MacroMappings)
	}
}
