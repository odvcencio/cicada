package project

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

const portableMidiScore = `cicada 2
track bass acid {}
track lead acid {}
midi {
  port keys
  port pads
  keys ch 1 -> lead
  keys cc 74 -> bass.cutoff 200Hz..4kHz takeover = pickup
  pads note c1 -> launch verse
  pads ch 2 note 60 -> stop
}
pattern phrase { 1 }
scene verse { bass = phrase lead = phrase }
song { verse }
`

func TestMidiSourceJSONSourceRoundTrip(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(portableMidiScore))
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, diagnostics := FromScore(score)
	if len(diagnostics) != 0 || p == nil {
		t.Fatalf("lower: %v", diagnostics)
	}
	if p.MIDI == nil || len(p.MIDI.Maps) != 4 {
		t.Fatalf("lost MIDI: %+v", p.MIDI)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("KeyStep")) {
		t.Fatal("local device name leaked")
	}
	restored, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	nextScore, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse source: %v\n%s", diagnostics, source)
	}
	next, diagnostics := FromScore(nextScore)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	nextJSON, err := CanonicalJSON(next)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, nextJSON) {
		t.Fatalf("MIDI round trip changed:\n%s\n%s", encoded, nextJSON)
	}
}
