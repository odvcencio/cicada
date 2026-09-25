package project

import (
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPatternBodySettingsMatchHeaderAttributes(t *testing.T) {
	metadata := "track bass acid {}\ntrack drums drums {}\n"
	legacy := metadata + "pattern riff acid steps=4 swing=56 gate=60 seed=7 { 1 . 3 . }\npattern beat drums steps=4 swing=54 { bd: x..X; }\nscene main { bass=riff drums=beat }\nsong { main }\n"
	modern := metadata + "pattern riff acid { swing=56% gate=60% seed=7 1 . 3 . }\npattern beat drums { swing=54% bd: x..X; }\nscene main { bass=riff drums=beat }\nsong { main }\n"
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
		t.Fatal("pattern body settings changed the semantic project")
	}
}
