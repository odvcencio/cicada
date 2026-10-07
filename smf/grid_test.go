package smf

import (
	"bytes"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"testing"
)

func TestTupletExportUsesExactTicks(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2 track bass acid {} pattern triplet { step = 1/8t 1 3 5 } scene main { bass = triplet } song { main }"))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	f, err := FromProject(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.PPQ != 960 || len(f.Tracks[1].Notes) != 12 {
		t.Fatalf("%+v", f)
	}
	for i, n := range f.Tracks[1].Notes {
		if n.Tick != int64(i*320) || n.Dur != 176 {
			t.Fatalf("note %d: %+v", i, n)
		}
	}
	var out bytes.Buffer
	if err := Encode(f, &out); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(&out)
	if err != nil {
		t.Fatal(err)
	}
	if err := MusicalDiff(f, decoded); err != nil {
		t.Fatal(err)
	}
}
