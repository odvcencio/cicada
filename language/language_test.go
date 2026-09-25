package language

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/notation"
)

var update = flag.Bool("update", false, "rewrite golden span listings")

// fixtures are the scores the query tests read: every example plus syntax
// the renderer does not support yet.
func fixtures(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("../examples/*.cicada")
	if err != nil {
		t.Fatal(err)
	}
	more, err := filepath.Glob("testdata/*.cicada")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte)
	for _, path := range append(paths, more...) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = src
	}
	return out
}

func parseFixture(t *testing.T, src []byte) (*gts.Tree, *compiled) {
	t.Helper()
	tree, q, err := parse(src)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tree.Release)
	if tree.RootNode().HasErrorOrMissing() {
		_, _, err := notation.ParseTree(src)
		t.Fatalf("fixture has syntax errors: %v", err)
	}
	return tree, q
}

func TestQueriesCompile(t *testing.T) {
	lang, err := notation.Language()
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{
		"highlights": HighlightsQuery, "locals": LocalsQuery, "tags": TagsQuery,
		"folds": FoldsQuery, "indents": IndentsQuery,
	} {
		if _, err := gts.NewQuery(query, lang); err != nil {
			t.Errorf("%s.scm: %v", name, err)
		}
	}
}

func leaves(n *gts.Node, visit func(*gts.Node)) {
	if n.ChildCount() == 0 {
		if n.EndByte() > n.StartByte() {
			visit(n)
		}
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		leaves(n.Child(i), visit)
	}
}

// TestFixturesUseEveryToken keeps the fixtures honest: a token added to the
// grammar must appear in a fixture, where the coverage test below requires a
// capture for it.
func TestFixturesUseEveryToken(t *testing.T) {
	lang, err := notation.Language()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, src := range fixtures(t) {
		tree, _ := parseFixture(t, src)
		leaves(tree.RootNode(), func(n *gts.Node) { seen[n.Type(lang)] = true })
	}
	for symbol := 1; symbol < int(lang.TokenCount); symbol++ {
		name := lang.SymbolNames[symbol]
		// Grammargen escapes punctuation in its symbol table, while CST node
		// types expose the literal character (for example, \\? versus ?).
		if !lang.SymbolMetadata[symbol].Visible || seen[name] || seen[strings.TrimPrefix(name, "\\")] {
			continue
		}
		t.Errorf("no fixture uses token %q", name)
	}
}

// TestEveryLeafHasOneCapture is the highlight contract: each token in a
// score gets exactly one capture, whatever the editor's precedence rule, and
// only the markup layers land on larger nodes.
func TestEveryLeafHasOneCapture(t *testing.T) {
	layers := map[string]bool{"markup.strong": true, "markup.italic": true, "markup.underline": true}
	for path, src := range fixtures(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			tree, q := parseFixture(t, src)
			captures := make(map[nodeKey][]string)
			for _, match := range q.highlights.Execute(tree) {
				for _, capture := range match.Captures {
					if capture.Node.ChildCount() > 0 && !layers[capture.Name] {
						t.Errorf("%s captures a whole %s", capture.Name, capture.Node.Type(q.lang))
					}
					captures[keyOf(capture.Node)] = append(captures[keyOf(capture.Node)], capture.Name)
				}
			}
			leaves(tree.RootNode(), func(n *gts.Node) {
				if got := captures[keyOf(n)]; len(got) != 1 {
					line, column := position(src, int(n.StartByte()))
					t.Errorf("%d:%d %s %q has captures %v", line, column, n.Type(q.lang), n.Text(src), got)
				}
			})
		})
	}
}

// TestHighlightGolden locks the capture of every token in the tour and in
// the syntax that runs ahead of the renderer. Run with -update after an
// intended query change and review the diff.
func TestHighlightGolden(t *testing.T) {
	for source, golden := range map[string]string{
		"../examples/cicada-chorus.cicada": "testdata/cicada-chorus.spans",
		"testdata/ahead.cicada":            "testdata/ahead.spans",
	} {
		src, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		spans, err := Highlight(src)
		if err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := WriteSpans(&got, src, spans); err != nil {
			t.Fatal(err)
		}
		if *update {
			if err := os.WriteFile(golden, got.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run go test ./language -update)", err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%s spans differ from %s; run go test ./language -update and review the diff", source, golden)
		}
	}
}

// captureOf returns the captures of spans whose text is exactly text and
// that start at line:column, outermost first.
func captureOf(t *testing.T, src []byte, spans []Span, line, column int, text string) []string {
	t.Helper()
	var out []string
	for _, span := range spans {
		l, c := position(src, span.Start)
		if l == line && c == column && string(src[span.Start:span.End]) == text {
			out = append(out, span.Capture)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no span %q at %d:%d", text, line, column)
	}
	return out
}

func TestHighlightMusicalConstructs(t *testing.T) {
	src := []byte(`cicada 1
key c# dorian
track kit drums { level = off }
pattern riff acid steps = 4 { 5,^~ c#3'*2%25 - | . }
pattern beat drums steps = 4 { bd: xX.x9; oh: x*2%50...; }
scene main { kit = beat riff = keep }
song { main*8 }
`)
	spans, err := Highlight(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		line, column int
		text         string
		want         []string
	}{
		{2, 5, "c#", []string{"constant.pitch.root"}},
		{2, 8, "dorian", []string{"type.builtin"}},
		{3, 7, "kit", []string{"variable.member"}},
		{3, 11, "drums", []string{"type.builtin"}},
		{3, 27, "off", []string{"boolean"}},
		{4, 9, "riff", []string{"function"}},
		{4, 14, "acid", []string{"type.builtin"}},
		{4, 19, "steps", []string{"attribute"}},
		{4, 31, "5,^~", []string{"markup.strong", "markup.italic"}},
		{4, 31, "5", []string{"constant.pitch.degree"}},
		{4, 32, ",", []string{"operator.octave.down"}},
		{4, 33, "^", []string{"operator.accent"}},
		{4, 34, "~", []string{"operator.slide"}},
		{4, 36, "c#3'*2%25", []string{"markup.underline"}},
		{4, 36, "c#3", []string{"constant.pitch.letter"}},
		{4, 39, "'", []string{"operator.octave.up"}},
		{4, 40, "*", []string{"operator.ratchet"}},
		{4, 41, "2", []string{"number.ratchet"}},
		{4, 42, "%", []string{"operator.probability"}},
		{4, 43, "25", []string{"number.probability"}},
		{4, 46, "-", []string{"punctuation.special.tie"}},
		{4, 48, "|", []string{"punctuation.delimiter.bar"}},
		{4, 50, ".", []string{"punctuation.special.rest"}},
		{5, 32, "bd", []string{"tag.builtin"}},
		{5, 36, "x", []string{"constant.hit"}},
		{5, 37, "X", []string{"markup.strong", "constant.hit.accent"}},
		{5, 38, ".", []string{"punctuation.special.rest"}},
		{5, 39, "x9", []string{"constant.hit.velocity"}},
		{5, 47, "x*2%50", []string{"markup.underline"}},
		{6, 14, "kit", []string{"variable.member"}},
		{6, 20, "beat", []string{"function"}},
		{6, 32, "keep", []string{"constant.builtin"}},
		{7, 8, "main", []string{"label"}},
		{7, 12, "*", []string{"operator.repeat"}},
		{7, 13, "8", []string{"number.bars"}},
	} {
		if got := captureOf(t, src, spans, c.line, c.column, c.text); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%d:%d %q = %v, want %v", c.line, c.column, c.text, got, c.want)
		}
	}
}

func TestLocalsResolveInstrumentNames(t *testing.T) {
	src := []byte(`cicada 1
instrument pluck {
  param bright: hz = 900hz;
  voice mono {
    let early = late;
    let late = saw(pitch);
    let shape = env(gate, 40ms);
    out = lowpass(late, bright) * shape * velocity;
  }
}
`)
	spans, err := Highlight(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		line, column int
		text, want   string
	}{
		{5, 17, "late", "variable"},             // used before its let: unresolved
		{6, 20, "pitch", "variable.builtin"},    // voice input
		{8, 19, "late", "variable"},             // let
		{8, 25, "bright", "variable.parameter"}, // parameter
		{8, 35, "shape", "variable"},
		{8, 43, "velocity", "variable.builtin"},
	} {
		got := captureOf(t, src, spans, c.line, c.column, c.text)
		if got[len(got)-1] != c.want {
			t.Errorf("%d:%d %q = %v, want %s", c.line, c.column, c.text, got, c.want)
		}
	}
}

func TestHighlightMarksSyntaxErrors(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {\n  cutoff = 620hz\n")
	spans, err := Highlight(src)
	if err != nil {
		t.Fatal(err)
	}
	var errors, keywords int
	for _, span := range spans {
		switch span.Capture {
		case ErrorCapture:
			errors++
		case "keyword.directive":
			keywords++
		}
	}
	if errors == 0 || keywords != 1 {
		t.Fatalf("want error spans and the cicada keyword, got %+v", spans)
	}
}

func TestSymbols(t *testing.T) {
	src, err := os.ReadFile("../examples/first-acid.cicada")
	if err != nil {
		t.Fatal(err)
	}
	symbols, err := Symbols(src)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range symbols {
		got = append(got, fmt.Sprintf("%d:%d %s %s %s", s.Position.Line, s.Position.Column, s.Role, s.Kind, s.Name))
	}
	for _, want := range []string{
		"11:7 definition track bass",
		"21:7 definition track drums",
		"27:12 definition instrument glassbass",
		"28:9 definition parameter cutoff",
		"32:9 definition binding sub",
		"33:20 reference binding osc",
		"35:24 reference parameter cutoff",
		"39:12 reference instrument glassbass",
		"43:8 definition phrase hook",
		"47:9 definition pattern bass-a",
		"52:7 reference phrase hook",
		"67:7 definition scene main",
		"68:3 reference track bass",
		"68:10 reference pattern bass-a",
		"80:3 reference scene main",
	} {
		found := false
		for _, line := range got {
			found = found || line == want
		}
		if !found {
			t.Errorf("missing symbol %q in:\n%s", want, strings.Join(got, "\n"))
		}
	}
	for _, line := range got {
		if strings.HasSuffix(line, "instrument acid") || strings.HasSuffix(line, "instrument drums") {
			t.Errorf("built-in voice tagged as an instrument reference: %s", line)
		}
	}
}

func TestAuthoredKitSymbols(t *testing.T) {
	src, err := os.ReadFile("../examples/authored-kit.cicada")
	if err != nil {
		t.Fatal(err)
	}
	symbols, err := Symbols(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"3:12 definition instrument kick": false,
		"9:5 definition kit steel":        false,
		"10:8 reference instrument kick":  false,
		"14:13 reference kit steel":       false,
	}
	for _, symbol := range symbols {
		key := fmt.Sprintf("%d:%d %s %s %s", symbol.Position.Line, symbol.Position.Column, symbol.Role, symbol.Kind, symbol.Name)
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("missing %s in %+v", key, symbols)
		}
	}
}

func TestFoldsAndIndents(t *testing.T) {
	src, err := os.ReadFile("../examples/cicada-chorus.cicada")
	if err != nil {
		t.Fatal(err)
	}
	tree, q := parseFixture(t, src)
	count := func(query string) map[string]int {
		compiled, err := gts.NewQuery(query, q.lang)
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]int)
		for _, match := range compiled.Execute(tree) {
			for _, capture := range match.Captures {
				out[capture.Name]++
			}
		}
		return out
	}
	// Every "{" opens a body that folds and indents, and every "}" closes one.
	braces := bytes.Count(src, []byte("{"))
	if got := count(FoldsQuery)["fold"]; got != braces {
		t.Errorf("folds = %d, want %d", got, braces)
	}
	indents := count(IndentsQuery)
	if indents["indent.begin"] != braces || indents["indent.end"] != braces || indents["indent.branch"] != braces {
		t.Errorf("indents = %v, want %d of each", indents, braces)
	}
}

func TestRunsLayerNestedStyles(t *testing.T) {
	src := []byte("cicada 1 pattern p acid { 1^ }")
	spans, err := Highlight(src)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.IndexByte(src, '^') - 1
	for _, r := range runs(src, spans, Night) {
		if r.start <= at && at < r.end {
			want, _ := degreeColor("1")
			if !r.style.Bold || r.style.Foreground != want {
				t.Fatalf("accented tonic style = %+v, want bold %+v", r.style, want)
			}
			return
		}
	}
	t.Fatal("no run covers the note")
}

func TestWriters(t *testing.T) {
	src := []byte("cicada 1\ntitle \"<drums & bass>\"\npattern p acid { 1^ }\n")
	spans, err := Highlight(src)
	if err != nil {
		t.Fatal(err)
	}
	var ansi bytes.Buffer
	if err := WriteANSI(&ansi, src, spans, Night); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(ansi.String())
	if plain != string(src) {
		t.Fatalf("ANSI output changes the text:\n%q\n%q", plain, src)
	}
	if !strings.Contains(ansi.String(), "\x1b[1;38;2;255;79;123m^\x1b[0m") {
		t.Fatalf("accent is not bold and pink: %q", ansi.String())
	}
	var page bytes.Buffer
	if err := WriteHTML(&page, src, spans, Night, "a <score>"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<title>a &lt;score&gt;</title>", "&#34;&lt;drums &amp; bass&gt;&#34;", "font-weight:700"} {
		if !strings.Contains(page.String(), want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	var listing bytes.Buffer
	if err := WriteSpans(&listing, []byte("title \"🎵\" tempo 90"), []Span{{Start: 17, End: 19, Capture: "number"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(listing.String(), "1:15 ") {
		t.Fatalf("span columns must count Unicode scalars: %q", listing.String())
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestThemeFallbackAndTints(t *testing.T) {
	if got := Night.Style("function.macro.phrase", ""); got != Night.Styles["function.macro"] {
		t.Errorf("fallback style = %+v", got)
	}
	tonic, _ := degreeColor("1")
	if !(tonic.R > tonic.B && tonic.G > tonic.B) {
		t.Errorf("tonic should be gold, got %+v", tonic)
	}
	if brightness(mustColor(letterColor("c6"))) <= brightness(mustColor(letterColor("c0"))) {
		t.Error("higher octaves should be lighter")
	}
	quiet, loud := mustColor(musicalTint("constant.hit.velocity", "x1")), mustColor(musicalTint("constant.hit.velocity", "x9"))
	if brightness(quiet) >= brightness(loud) {
		t.Error("louder hits should glow brighter")
	}
	rare, sure := mustColor(musicalTint("number.probability", "5")), mustColor(musicalTint("number.probability", "95"))
	if brightness(rare) >= brightness(sure) {
		t.Error("likelier steps should be brighter")
	}
	if _, ok := musicalTint("constant.pitch.degree", "9"); ok {
		t.Error("an invalid degree should keep the theme color")
	}
}

func mustColor(c Color, ok bool) Color {
	if !ok {
		panic("no tint")
	}
	return c
}

func brightness(c Color) int { return int(c.R) + int(c.G) + int(c.B) }

// TestHighlighterAndTaggerAPIs checks the gotreesitter-facing constructors
// that editors embed.
func TestHighlighterAndTaggerAPIs(t *testing.T) {
	src := []byte("cicada 1\ntrack bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n")
	highlighter, err := NewHighlighter()
	if err != nil {
		t.Fatal(err)
	}
	var captures []string
	for _, r := range highlighter.Highlight(src) {
		captures = append(captures, r.Capture)
	}
	if !strings.Contains(strings.Join(captures, " "), "constant.pitch.degree") {
		t.Errorf("flat highlighter captures = %v", captures)
	}
	tagger, err := NewTagger()
	if err != nil {
		t.Fatal(err)
	}
	if tags := tagger.Tag(src); len(tags) != 6 {
		t.Errorf("tags = %+v, want track, pattern, scene, two scene references, and a song reference", tags)
	}
}
