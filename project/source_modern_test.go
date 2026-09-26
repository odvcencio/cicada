package project

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestToSourceUsesConciseMusicianNotation(t *testing.T) {
	legacy := []byte("cicada 1\ntrack bass acid {}\npattern riff acid steps=2 swing=56 gate=60 { 1%70 . }\nscene main { bass = riff }\nscene hold { bass = keep }\nsong { main hold }\n")
	score, diagnostics := notation.Parse(legacy)
	if len(diagnostics) != 0 {
		t.Fatalf("legacy diagnostics: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("project diagnostics: %+v", diagnostics)
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, redundant := range [][]byte{[]byte("cicada 1"), []byte("pattern riff acid"), []byte("steps ="), []byte("bass = keep"), []byte("tempo 130"), []byte("key a minor"), []byte("1%70")} {
		if bytes.Contains(source, redundant) {
			t.Fatalf("generated source retained %q:\n%s", redundant, source)
		}
	}
	for _, wanted := range [][]byte{[]byte("pattern riff {\n  swing = 56%\n  gate = 60%"), []byte("a2?70 ."), []byte("scene hold {\n}")} {
		if !bytes.Contains(source, wanted) {
			t.Fatalf("generated source omitted %q:\n%s", wanted, source)
		}
	}
	reparsed, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("generated source diagnostics: %+v", diagnostics)
	}
	again, diagnostics := FromScore(reparsed)
	if again == nil || len(diagnostics) != 0 || !SemanticEqual(p, again) {
		t.Fatalf("generated source changed musical meaning: %+v", diagnostics)
	}
}
