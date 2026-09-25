package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestChanceNotationMatchesLegacyProbability(t *testing.T) {
	legacy := "track bass acid {}\ntrack drums drums {}\npattern riff acid steps=4 { 1%70 . 5%25 . }\npattern beat drums steps=4 { bd: x%50..X; }\nscene main { bass=riff drums=beat }\nsong { main }\n"
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
	oldProject := parse(legacy)
	newProject := parse(strings.ReplaceAll(legacy, "%", "?"))
	if !SemanticEqual(oldProject, newProject) {
		t.Fatal("chance spelling changed the compiled project")
	}
}
