package project

import (
	"bytes"
	"m31labs.dev/cicada/notation"
	"testing"
)

func TestArrangementMoveRepeatTrimExactRoundTrip(t *testing.T) {
	source := []byte(`cicada 2
track bass acid { octave = 4 }
pattern hits { c . c . }
arrange {
  // Preserve the placement identity and surrounding source.
  place bass-1 bass hits { at = @2.3.4 length = 1/2 }
  place bass-2 bass hits { at = @4.1.1 length = 2bars }
  marker chorus { at = @5.1.1 }
}
`)
	moved, err := notation.SetPlacementField(source, "bass-1", "at", "@3.1.1")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := notation.SetPlacementField(moved, "bass-1", "at", "@2.3.4")
	if err != nil || !bytes.Equal(source, restored) {
		t.Fatal("move failed exact source round-trip", err)
	}
	trimmed, err := notation.SetPlacementField(moved, "bass-1", "length", "240ticks")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(trimmed)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	if p.Tracks[0].Slots[0] == nil || *p.Tracks[0].Slots[0] != "hits" || p.Patterns[0].Data[0].Note != 60 {
		t.Fatalf("placement lost track octave or slot: %+v", p)
	}
	before, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(before)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	again, ds := notation.Parse(text)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	after, ds := FromScore(again)
	if after == nil {
		t.Fatal(ds)
	}
	canonical, err := CanonicalJSON(after)
	if err != nil || !bytes.Equal(before, canonical) {
		t.Fatal("arrangement semantic round-trip changed", err)
	}
	for _, bad := range []string{"song { main }\n", "arrange {}\n"} {
		if _, ds := notation.Parse(append(source, []byte(bad)...)); !hasErrors(ds) {
			t.Fatal("accepted ambiguous arrangement authority")
		}
	}
}
