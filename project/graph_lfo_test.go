package project

import (
	"bytes"
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

func TestGraphLFOSourceJSONAndImageRoundTrip(t *testing.T) {
	text := `cicada 2
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * lfo(1Hz, triangle, note)) } }
track voice lead {}
pattern p { c4 . }
scene main { voice = p }
song { main }
`
	score, ds := notation.Parse([]byte(text))
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
	if err != nil || !bytes.Contains(source, []byte("lfo(1Hz, triangle, note)")) {
		t.Fatalf("source: %s: %v", source, err)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.HasLFO(&cfg.Track[0].Graph) {
		t.Fatal("compiled graph has no LFO")
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if flags := binary.LittleEndian.Uint32(image[8:]); flags&kernelimage.GraphLFOFlag == 0 {
		t.Fatalf("missing graph LFO flag: %#x", flags)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || decoded.Track[0].Graph != cfg.Track[0].Graph {
		t.Fatalf("image round trip: %v", err)
	}
	bad := append([]byte(nil), image...)
	binary.LittleEndian.PutUint32(bad[8:], binary.LittleEndian.Uint32(bad[8:]) & ^kernelimage.GraphLFOFlag)
	if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
		t.Fatal("image accepted an LFO without its flag")
	}
}
