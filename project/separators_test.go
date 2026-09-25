package project

import (
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestSeparatorFreeKitAndInstrumentMatchLegacySource(t *testing.T) {
	legacy, err := os.ReadFile("../examples/authored-kit.cicada")
	if err != nil {
		t.Fatal(err)
	}
	modern := strings.ReplaceAll(string(legacy), ";", "")
	parse := func(source string) *Project {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("parse diagnostics: %+v", diagnostics)
		}
		project, diagnostics := FromScore(score)
		if project == nil || len(diagnostics) != 0 {
			t.Fatalf("project diagnostics: %+v", diagnostics)
		}
		return project
	}
	legacyProject := parse(string(legacy))
	if !SemanticEqual(legacyProject, parse(modern)) {
		t.Fatal("removing statement terminators changed the project")
	}
	document, err := notation.ParseDocument(legacy)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(formatted), ";") || !strings.Contains(string(formatted), "bd: x...\n  ch: ..x.") {
		t.Fatalf("formatter did not separate rows without terminators:\n%s", formatted)
	}
	if !SemanticEqual(legacyProject, parse(string(formatted))) {
		t.Fatal("formatter changed the project while removing terminators")
	}
	generated, err := ToSource(legacyProject)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(generated), ";") || !SemanticEqual(legacyProject, parse(string(generated))) {
		t.Fatalf("generated source changed the project or kept terminators:\n%s", generated)
	}
}

func TestShortPhraseTransposeMatchesKeyword(t *testing.T) {
	legacy := "track bass acid {}\nphrase hook { 1 . }\npattern riff { use hook transpose=7 }\nscene main { bass=riff }\nsong { main }\n"
	modern := strings.Replace(legacy, "transpose=7", "+7", 1)
	parse := func(source string) *Project {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("parse diagnostics: %+v", diagnostics)
		}
		project, diagnostics := FromScore(score)
		if project == nil || len(diagnostics) != 0 {
			t.Fatalf("project diagnostics: %+v", diagnostics)
		}
		return project
	}
	if !SemanticEqual(parse(legacy), parse(modern)) {
		t.Fatal("short phrase transpose changed the project")
	}
}
