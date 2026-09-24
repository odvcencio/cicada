package project

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestDryMixerSourceAndProjectRoundTrip(t *testing.T) {
	const source = `cicada 1
tempo 120
key a minor
track bass acid { level = -12db pan = -1 }
pattern riff acid steps=4 { 1 . . . }
scene main { bass=riff }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", diagnostics)
	}
	if p.Tracks[0].Mixer.GainDB != -12 || p.Tracks[0].Mixer.Pan != -1 || len(p.Tracks[0].Params) != 0 {
		t.Fatalf("mixer lowering: %+v", p.Tracks[0])
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rewritten, []byte("level = -12db")) || !bytes.Contains(rewritten, []byte("pan = -1")) {
		t.Fatalf("mixer disappeared: %s", rewritten)
	}
}

func TestDryMixerBoundsAndUnsupportedRouting(t *testing.T) {
	for _, params := range [][]notation.Param{
		{{Name: "level", Value: "7db"}},
		{{Name: "level", Value: "-61db"}},
		{{Name: "pan", Value: "1.1"}},
		{{Name: "pan", Value: "20hz"}},
	} {
		if _, err := CompileMixerParams(notation.Track{Params: params}); err == nil {
			t.Fatalf("accepted %+v", params)
		}
	}
	mixer, err := CompileMixerParams(notation.Track{Params: []notation.Param{{Name: "level", Value: "off"}}})
	if err != nil || !mixer.Mute {
		t.Fatalf("off fader: %+v, %v", mixer, err)
	}
}
