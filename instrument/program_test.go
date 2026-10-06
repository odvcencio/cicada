package instrument

import (
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestGlassbassGraph(t *testing.T) {
	src, err := os.ReadFile("../testdata/edition1/examples/first-acid.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(src)
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	program, ds := Compile(score.Instruments[0])
	if len(ds) != 0 {
		t.Fatalf("instrument diagnostics: %+v", ds)
	}
	if program.Name != "glassbass" || program.StatefulNodes != 4 || program.Output < 0 {
		t.Fatalf("wrong graph: %+v", program)
	}
	if program.Nodes[program.Output].Type != Audio || program.Nodes[program.Output].Op != "*" {
		t.Fatalf("wrong output node: %+v", program.Nodes[program.Output])
	}
	kernelProgram, err := Lower(program, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kernelProgram.GlideMS != 60 {
		t.Fatalf("custom graph glide default is %g ms, want 60 ms", kernelProgram.GlideMS)
	}
}

func TestUndefinedSymbol(t *testing.T) {
	src := []byte("cicada 1 instrument x { voice mono { out = saw(missing); } } track t x {} pattern p notes steps=1 { 1 } scene s { t=p } song { s }")
	score, ds := notation.Parse(src)
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	_, ds = Compile(score.Instruments[0])
	if len(ds) == 0 || ds[0].Code != "CICADA-REFERENCE" {
		t.Fatalf("expected unknown symbol error, got %+v", ds)
	}
}

func TestWrongArgumentType(t *testing.T) {
	src := []byte("cicada 1 instrument x { voice mono { out = saw(gate); } } track t x {} pattern p notes steps=1 { 1 } scene s { t=p } song { s }")
	score, ds := notation.Parse(src)
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	_, ds = Compile(score.Instruments[0])
	if len(ds) == 0 || ds[0].Code != "CICADA-PARAM" {
		t.Fatalf("expected function type error, got %+v", ds)
	}
}

func TestLowerRejectsUnknownOverride(t *testing.T) {
	src := []byte("cicada 1 instrument x { param cutoff: hz = 200hz; voice mono { out = saw(cutoff); } } track t x {} pattern p notes steps=1 { 1 } scene s { t=p } song { s }")
	score, diagnostics := notation.Parse(src)
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	program, diagnostics := Compile(score.Instruments[0])
	if program == nil || len(diagnostics) != 0 {
		t.Fatalf("instrument diagnostics: %+v", diagnostics)
	}
	if _, err := Lower(program, map[string]string{"ghost": "300hz"}); err == nil {
		t.Fatal("unknown instrument override was silently ignored")
	}
}

func TestExpressionInputsCompileOnlyWhenUsed(t *testing.T) {
	source := []byte("cicada 1 instrument expressive { voice mono { out = saw(pitch) * pressure * timbre * (1 + pitch_bend / 9600); } } track t expressive {} pattern p notes steps=1 { 1 } scene s { t=p } song { s }")
	score, ds := notation.Parse(source)
	if len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	program, ds := Compile(score.Instruments[0])
	if len(ds) != 0 {
		t.Fatalf("expression inputs: %+v", ds)
	}
	lowered, err := Lower(program, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, node := range program.Nodes {
		if node.Op == "input" {
			found[node.Name] = true
		}
	}
	if !found["pitch_bend"] || !found["pressure"] || !found["timbre"] || lowered.Len != uint8(len(program.Nodes)) {
		t.Fatal("expression inputs missing")
	}
}
