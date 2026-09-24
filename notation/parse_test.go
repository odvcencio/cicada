package notation

import (
	"os"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
)

func TestFirstAcidScore(t *testing.T) {
	src, err := os.ReadFile("../examples/first-acid.cicada")
	if err != nil {
		t.Fatal(err)
	}
	s, ds := Parse(src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %+v", ds)
	}
	if s.Version != 1 || s.TempoMilli != 138000 || s.KeyRoot != "a" || s.Scale != "minor" {
		t.Fatalf("wrong header: %+v", s)
	}
	if len(s.Instruments) != 1 || len(s.Tracks) != 3 || len(s.Phrases) != 1 || len(s.Patterns) != 4 || len(s.Scenes) != 2 || len(s.Song) != 2 {
		t.Fatalf("wrong score shape: %+v", s)
	}
	if len(s.Patterns[0].Steps) != 16 || len(s.Patterns[3].Lanes) != 4 {
		t.Fatalf("wrong pattern shape: %+v", s.Patterns)
	}
	for _, lane := range s.Patterns[3].Lanes {
		if len(lane.Hits) != 16 {
			t.Fatalf("lane %s has %d hits", lane.Name, len(lane.Hits))
		}
	}
	if s.Song[0].Scene != "main" || s.Song[0].Bars != 8 || s.Song[1].Scene != "outro" || s.Song[1].Bars != 8 {
		t.Fatalf("wrong song: %+v", s.Song)
	}
	if len(s.Patterns[1].Steps) != 16 || s.Patterns[1].Steps[8].Transpose != 12 || len(s.Patterns[1].Parts) != 2 {
		t.Fatalf("phrase expansion failed: %+v", s.Patterns[1])
	}
	if s.Instruments[0].Name != "glassbass" || len(s.Instruments[0].Lets) != 4 || s.Instruments[0].Output == nil {
		t.Fatalf("custom instrument AST missing: %+v", s.Instruments)
	}
}

func TestUnknownParameterHasPosition(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {\n  mystery = 42\n}\npattern a acid steps=1 { 1 }\nscene main { bass=a }\nsong { main }\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-PARAM" || ds[0].Position.Line != 3 {
		t.Fatalf("expected line 3 unknown parameter, got %+v", ds)
	}
}

func TestSlideIntoRestWarning(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {}\npattern a acid steps=2 { 1~ . }\nscene main { bass=a }\nsong { main }\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-SLIDE-REST" || ds[0].Severity != "warning" {
		t.Fatalf("expected slide warning, got %+v", ds)
	}
}

func TestPatternEndSlideWarningAllowsSceneTarget(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {}\npattern a acid steps=2 { . 1~ }\npattern b acid steps=2 { 5 . }\nscene first { bass=a }\nscene second { bass=b }\nsong { first second }\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-SLIDE-REST" || !strings.Contains(ds[0].Message, "switch may supply one") {
		t.Fatalf("expected scene-aware slide warning, got %+v", ds)
	}
}

func TestSyntaxErrorPosition(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {\n  cutoff = 620hz\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-SYNTAX" || !strings.Contains(ds[0].Message, "syntax error") {
		t.Fatalf("expected syntax error, got %+v", ds)
	}
}

func TestSharpKeyAndModifiers(t *testing.T) {
	src := []byte("cicada 1 key c# minor track bass acid {} pattern a acid steps=2 { 1^~*2%50 c#3 } scene main { bass=a } song { main }")
	s, ds := Parse(src)
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %+v", ds)
	}
	if s.KeyRoot != "c#" || s.Patterns[0].Steps[0].Text != "1^~*2%50" {
		t.Fatalf("wrong parsed pitch: %+v", s)
	}
}

func TestCommentsAndHighlightQuery(t *testing.T) {
	src := []byte("cicada 1\n// notes for the next bar\ntrack bass acid {}\npattern a acid steps=1 { 1 }\nscene main { bass=a }\nsong { main }\n")
	root, walker, err := ParseTree(src)
	if err != nil {
		t.Fatal(err)
	}
	foundComment := false
	for i := 0; i < root.NamedChildCount(); i++ {
		if walker.Type(root.NamedChild(i)) == "comment" {
			foundComment = true
		}
	}
	if !foundComment {
		t.Fatal("comment missing from CST")
	}
	query, err := os.ReadFile("../language/highlights.scm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gts.NewQuery(string(query), walker.Lang); err != nil {
		t.Fatalf("highlight query: %v", err)
	}
}

func TestUnsupportedAndSeedDiagnostics(t *testing.T) {
	cases := []struct {
		name, source, code string
	}{
		{"large project seed", "seed 4294967296", "CICADA-SEED"},
		{"large pattern seed", "", "CICADA-SEED"},
		{"effect", "fx echo {}", "CICADA-UNSUPPORTED"},
		{"mixer value", "track bass acid { send_a = 0.5 }", "CICADA-UNSUPPORTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pattern := "pattern a acid steps=1 { 1 }"
			if tc.name == "large pattern seed" {
				pattern = "pattern a acid steps=1 seed=4294967296 { 1 }"
			}
			track := "track bass acid {}"
			if tc.name == "mixer value" {
				track = tc.source
			}
			src := []byte("cicada 1\n" + tc.source + "\n" + track + "\n" + pattern + "\nscene main { bass=a }\nsong { main }\n")
			_, ds := Parse(src)
			for _, d := range ds {
				if d.Code == tc.code && d.Position.Line > 0 {
					return
				}
			}
			t.Fatalf("want %s with position, got %+v", tc.code, ds)
		})
	}
}

func TestM1DrumLanesAreRejectedUntilImplemented(t *testing.T) {
	for _, body := range []string{
		"track kit drums { lt_tune = 1 }\npattern beat drums steps=4 { bd: x...; }",
		"track kit drums {}\npattern beat drums steps=4 { lt: x...; }",
	} {
		source := "cicada 1\ntempo 120\nkey a minor\n" + body + "\nscene main { kit=beat }\nsong { main }\n"
		_, diagnostics := Parse([]byte(source))
		found := false
		for _, d := range diagnostics {
			if d.Code == "CICADA-UNSUPPORTED" {
				found = true
			}
		}
		if !found {
			t.Fatalf("M1 lane accepted: %s, diagnostics=%+v", body, diagnostics)
		}
	}
}

func TestPentatonicMissingDegreeDiagnostic(t *testing.T) {
	src := []byte("cicada 1\nkey a pent\ntrack bass acid {}\npattern a acid steps=1 { 2 }\nscene main { bass=a }\nsong { main }\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-SCALE-DEGREE" || ds[0].Position.Line != 4 {
		t.Fatalf("wrong pentatonic diagnostics: %+v", ds)
	}
}
