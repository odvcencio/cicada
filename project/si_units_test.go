package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestSIUnitsMatchLegacyLiterals(t *testing.T) {
	modern := "instrument tone { param cutoff: hz = 720Hz; voice mono { out = saw(440Hz); } }\ntrack lead tone { cutoff = 2kHz level = -6dB }\npattern riff { 1 . }\nscene main { lead = riff }\nsong { main }\n"
	legacy := strings.NewReplacer("720Hz", "720hz", "440Hz", "440hz", "2kHz", "2khz", "-6dB", "-6db").Replace(modern)
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
		if _, err := CompileEngine(project, 48_000, 128); err != nil {
			t.Fatalf("engine compilation: %v", err)
		}
		return project
	}
	if !SemanticEqual(parse(modern), parse(legacy)) {
		t.Fatal("SI spelling changed the semantic project")
	}
}
