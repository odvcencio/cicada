package smf

import (
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"testing"
)

func TestSourceChainMIDIExactTicks(t *testing.T) {
	source, err := os.ReadFile("../examples/section-chain.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
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
	for i, want := range []int64{0, 240, 560, 880, 1200, 1680, 2160, 2400, 2720, 3040, 3360} {
		if i >= len(f.Tracks[1].Notes) || f.Tracks[1].Notes[i].Tick != want {
			t.Fatalf("%+v", f.Tracks[1].Notes)
		}
	}
}
