package notation

import (
	"bytes"
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

func TestUnicodeScalarDiagnosticColumns(t *testing.T) {
	duplicate := []byte("cicada 1\ntitle \"🎵\" title \"second\"\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	_, diagnostics := Parse(duplicate)
	found := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "CICADA-DUPLICATE" && diagnostic.Position == (Position{Line: 2, Column: 11}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("semantic column counted UTF-8 bytes: %+v", diagnostics)
	}
	_, diagnostics = Parse([]byte("cicada 1\ntitle \"🎵\" @\n"))
	if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-SYNTAX" || diagnostics[0].Position != (Position{Line: 2, Column: 11}) || strings.Contains(diagnostics[0].Message, "2:14:") {
		t.Fatalf("syntax column counted UTF-8 bytes: %+v", diagnostics)
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

func TestAllDrumLanesParse(t *testing.T) {
	for _, body := range []string{
		"track kit drums { lt_tune = 1 }\npattern beat drums steps=4 { bd: x...; }",
		"track kit drums {}\npattern beat drums steps=4 { lt: x...; }",
	} {
		source := "cicada 1\ntempo 120\nkey a minor\n" + body + "\nscene main { kit=beat }\nsong { main }\n"
		_, diagnostics := Parse([]byte(source))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatalf("drum lane rejected: %s, diagnostics=%+v", body, diagnostics)
			}
		}
	}
}

func TestAuthoredKitKeepsBindingsAndParses(t *testing.T) {
	source, err := os.ReadFile("../examples/authored-kit.cicada")
	if err != nil {
		t.Fatal(err)
	}
	document, err := ParseDocument(source)
	if err != nil || !bytes.Equal(Print(document), source) {
		t.Fatalf("kit CST print changed source: %v", err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseDocument(formatted)
	if err != nil {
		t.Fatalf("formatted kit is not grammar-valid: %v\n%s", err, formatted)
	}
	again, err := Format(reparsed)
	if err != nil || !bytes.Equal(formatted, again) {
		t.Fatalf("kit formatting is not stable: %v\n%s\n%s", err, formatted, again)
	}
	score, diagnostics := Parse(source)
	if score == nil || len(score.Kits) != 1 || len(score.Kits[0].Bindings) != 2 {
		t.Fatalf("kit bindings were not lowered: %+v, diagnostics=%+v", score, diagnostics)
	}
	if kit := score.Kits[0]; kit.Name != "steel" || kit.Bindings[0].Lane != "bd" || kit.Bindings[0].Target != "kick" || kit.Bindings[1].Lane != "ch" || kit.Bindings[1].Target != "builtin.ch" {
		t.Fatalf("kit binding changed: %+v", kit)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("valid authored kit has diagnostics: %+v", diagnostics)
	}
}

func TestAuthoredKitRejectsInvalidBindings(t *testing.T) {
	source, err := os.ReadFile("../examples/authored-kit.cicada")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, bindings, code string
	}{
		{"duplicate lane", "bd=kick; bd=builtin.bd;", "CICADA-DUPLICATE"},
		{"unknown instrument", "bd=missing; ch=builtin.ch;", "CICADA-REFERENCE"},
		{"unknown built-in", "bd=kick; ch=builtin.zz;", "CICADA-REFERENCE"},
		{"unknown lane", "zz=kick; ch=builtin.ch;", "CICADA-REFERENCE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := strings.Replace(string(source), "bd=kick; ch=builtin.ch;", test.bindings, 1)
			_, diagnostics := Parse([]byte(changed))
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == test.code && diagnostic.Position.Line == 3 {
					return
				}
			}
			t.Fatalf("want %s at kit line, got %+v", test.code, diagnostics)
		})
	}
}

func TestPentatonicMissingDegreeDiagnostic(t *testing.T) {
	src := []byte("cicada 1\nkey a pent\ntrack bass acid {}\npattern a acid steps=1 { 2 }\nscene main { bass=a }\nsong { main }\n")
	_, ds := Parse(src)
	if len(ds) != 1 || ds[0].Code != "CICADA-SCALE-DEGREE" || ds[0].Position.Line != 4 {
		t.Fatalf("wrong pentatonic diagnostics: %+v", ds)
	}
}

func TestSongMustHaveAnEntryBeforeValidationSucceeds(t *testing.T) {
	for _, tc := range []struct {
		name, ending string
		line         int
	}{
		{"missing declaration", "", 1},
		{"empty declaration", "song {}\n", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte("cicada 1\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\n" + tc.ending)
			_, diagnostics := Parse(src)
			for _, d := range diagnostics {
				if d.Code == "CICADA-LIMIT" && d.Severity == "error" && d.Position.Line == tc.line {
					return
				}
			}
			t.Fatalf("empty song was accepted: %+v", diagnostics)
		})
	}
}

func TestDuplicateSingletonDeclarationsAreRejected(t *testing.T) {
	body := "track bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\n"
	for _, tc := range []struct {
		name, source string
		line         int
	}{
		{"title", "title \"first\"\ntitle \"second\"\n" + body + "song { main }\n", 3},
		{"tempo", "tempo 120\ntempo 130\n" + body + "song { main }\n", 3},
		{"key", "key a minor\nkey c major\n" + body + "song { main }\n", 3},
		{"seed", "seed 1\nseed 2\n" + body + "song { main }\n", 3},
		{"song", body + "song { main }\nsong { main }\n", 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diagnostics := Parse([]byte("cicada 1\n" + tc.source))
			for _, d := range diagnostics {
				if d.Code == "CICADA-DUPLICATE" && d.Position.Line == tc.line {
					return
				}
			}
			t.Fatalf("duplicate %s accepted: %+v", tc.name, diagnostics)
		})
	}
}

func TestSourceRecordLimitsMatchProjectFormat(t *testing.T) {
	long := strings.Repeat("a", 65)
	for _, tc := range []struct {
		name, source string
		line         int
	}{
		{"title", "title \"" + strings.Repeat("🎵", 121) + "\"\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n", 2},
		{"track ID", "track " + long + " acid {}\npattern p acid steps=1 { 1 }\nscene main { " + long + "=p }\nsong { main }\n", 2},
		{"pattern ID", "track bass acid {}\npattern " + long + " acid steps=1 { 1 }\nscene main { bass=" + long + " }\nsong { main }\n", 3},
		{"scene ID", "track bass acid {}\npattern p acid steps=1 { 1 }\nscene " + long + " { bass=p }\nsong { " + long + " }\n", 4},
		{"instrument ID", "instrument " + long + " { voice mono { out = saw(pitch); } }\ntrack bass " + long + " {}\npattern p notes steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n", 2},
		{"phrase ID", "track bass acid {}\nphrase " + long + " acid { 1 }\npattern p acid steps=1 { use " + long + " }\nscene main { bass=p }\nsong { main }\n", 3},
		{"instrument parameter", "instrument tone { param " + long + ": hz = 200hz; voice mono { out = saw(pitch); } }\ntrack bass tone {}\npattern p notes steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n", 2},
		{"instrument binding", "instrument tone { voice mono { let " + long + " = saw(pitch); out = " + long + "; } }\ntrack bass tone {}\npattern p notes steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diagnostics := Parse([]byte("cicada 1\n" + tc.source))
			for _, d := range diagnostics {
				if d.Code == "CICADA-LIMIT" && d.Position.Line == tc.line {
					return
				}
			}
			t.Fatalf("source limit was accepted: %+v", diagnostics)
		})
	}
	valid := []byte("cicada 1\ntitle \"" + strings.Repeat("🎵", 120) + "\"\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	if _, diagnostics := Parse(valid); len(diagnostics) != 0 {
		t.Fatalf("120 Unicode-scalar title was rejected: %+v", diagnostics)
	}
	id := strings.Repeat("a", 64)
	validID := []byte("cicada 1\ntrack " + id + " acid {}\npattern p acid steps=1 { 1 }\nscene main { " + id + "=p }\nsong { main }\n")
	if _, diagnostics := Parse(validID); len(diagnostics) != 0 {
		t.Fatalf("64-byte identifier was rejected: %+v", diagnostics)
	}
	badEscape := []byte("cicada 1\ntitle \"\\q\"\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	_, diagnostics := Parse(badEscape)
	for _, d := range diagnostics {
		if d.Code == "CICADA-SYNTAX" && d.Position.Line == 2 {
			return
		}
	}
	t.Fatalf("invalid title escape was accepted: %+v", diagnostics)
}
