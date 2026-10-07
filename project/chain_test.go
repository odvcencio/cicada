package project

import (
	"m31labs.dev/cicada/notation"
	"os"
	"testing"
)

func TestSourceSectionChainRoundTripAndReferences(t *testing.T) {
	source, err := os.ReadFile("../examples/section-chain.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	if len(p.Tracks[0].Chain) != 3 {
		t.Fatalf("%+v", p.Tracks[0])
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Patterns[0].Chain) != 3 {
		t.Fatalf("%+v", cfg.Patterns[0].Chain)
	}
	if _, err := ToSource(p); err != nil {
		t.Fatal(err)
	}
	_, ds = notation.Parse([]byte("track bass acid {chain=missing} pattern riff {1} scene main {} song {main}"))
	found := false
	for _, d := range ds {
		found = found || d.Code == "CICADA-REFERENCE"
	}
	if !found {
		t.Fatalf("%v", ds)
	}
}
