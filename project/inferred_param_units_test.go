package project

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestInferredInstrumentParamUnitsPreserveSemantics(t *testing.T) {
	modern := "instrument tone { param cutoff = 720Hz param decay = 0.3s param level = -6dB param amount = 50% voice mono { out = saw(cutoff) * amount } }\n" +
		"track lead tone { cutoff = 2kHz }\npattern riff { 1 . }\nscene main { lead = riff }\nsong { main }\n"
	legacy := strings.NewReplacer(
		"param cutoff =", "param cutoff: hz =",
		"param decay =", "param decay: ms =",
		"param level =", "param level: db =",
		"param amount =", "param amount: unit =",
	).Replace(modern)
	parse := func(source string) *Project {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("parse diagnostics: %+v", diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("project diagnostics: %+v", diagnostics)
		}
		if _, err := CompileEngine(p, 48_000, 128); err != nil {
			t.Fatalf("engine compilation: %v", err)
		}
		return p
	}
	modernProject := parse(modern)
	if !SemanticEqual(modernProject, parse(legacy)) {
		t.Fatal("inferred parameter units changed the semantic project")
	}
	generated, err := ToSource(modernProject)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated, []byte("param cutoff = 720Hz")) || bytes.Contains(generated, []byte("param cutoff:")) {
		t.Fatalf("generated source retained redundant parameter type: %s", generated)
	}
}
