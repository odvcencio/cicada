package instrument

import (
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
	"os"
	"strings"
	"testing"
)

func TestDDSPScoreAndUnits(t *testing.T) {
	src, err := os.ReadFile("../examples/neural-reed.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(src)
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	p, ds := Compile(score.Instruments[0])
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	gp, err := Lower(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gp.Nodes[gp.Output].Op != graph.DDSP {
		t.Fatal("DDSP was not lowered")
	}
	bad := strings.ReplaceAll(string(src), "ddsp(pitch, loudness)", "ddsp(loudness, pitch)")
	score, ds = notation.Parse([]byte(bad))
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	if _, ds = Compile(score.Instruments[0]); len(ds) == 0 {
		t.Fatal("incorrect DDSP units accepted")
	}
}
