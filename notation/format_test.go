package notation

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSourcePrintAndFormat(t *testing.T) {
	source := []byte("cicada 1\r\n// Keep this comment\r\ntrack bass acid {}\r\npattern a acid steps=2 { 1^~*2%50 . }\r\nscene main { bass=a }\r\nsong { main }\r\n")
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(Print(document), source) {
		t.Fatal("source printer changed bytes")
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), "// Keep this comment") || !strings.Contains(string(formatted), "1^~*2%50") {
		t.Fatalf("formatter lost authored content:\n%s", formatted)
	}
	if bytes.Contains(formatted, []byte("\r")) || !bytes.HasSuffix(formatted, []byte("\n")) {
		t.Fatalf("formatter did not normalize newlines:\n%s", formatted)
	}
	reparsed, ds := Parse(formatted)
	if reparsed == nil {
		t.Fatalf("formatted score did not parse: %+v\n%s", ds, formatted)
	}
	for _, diagnostic := range ds {
		if diagnostic.Severity == "error" {
			t.Fatalf("formatted score is invalid: %+v\n%s", ds, formatted)
		}
	}
	againDocument, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Format(againDocument)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatted, again) {
		t.Fatalf("formatter is not idempotent:\n%s\n---\n%s", formatted, again)
	}
}

func TestFormatKeepsOctaveMarksAndGroupingParens(t *testing.T) {
	source := []byte("cicada 1\ninstrument sub {\n  voice mono {\n    let shape = env(gate, 90ms);\n    out = saw(pitch)*( shape * velocity );\n  }\n}\ntrack low sub {}\npattern a notes steps=4 { 7,~ 5,,^*2 3, ~%50 c2, }\nscene main { low=a }\nsong { main }\n")
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"out = saw(pitch) * (shape * velocity);",
		"pattern a notes steps = 4 {\n  7,~ 5,,^*2 3,~%50 c2,\n}",
	} {
		if !strings.Contains(string(formatted), want) {
			t.Fatalf("formatted score lacks %q:\n%s", want, formatted)
		}
	}
	original, ds := Parse(source)
	if len(ds) != 0 {
		t.Fatalf("source diagnostics: %+v", ds)
	}
	reparsed, ds := Parse(formatted)
	if len(ds) != 0 {
		t.Fatalf("formatted diagnostics: %+v\n%s", ds, formatted)
	}
	for i, step := range original.Patterns[0].Steps {
		if got := reparsed.Patterns[0].Steps[i].Text; got != strings.ReplaceAll(step.Text, " ", "") {
			t.Fatalf("step %d = %q, want %q", i, got, step.Text)
		}
	}
}

func TestExamplesRemainValidAfterFormat(t *testing.T) {
	for _, name := range []string{"first-acid", "glassbass", "circuit-kit", "acid-voice"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../examples/" + name + ".cicada")
			if err != nil {
				t.Fatal(err)
			}
			document, err := ParseDocument(source)
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := Format(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, ds := Parse(formatted); len(ds) != 0 {
				t.Fatalf("formatted score diagnostics: %+v\n%s", ds, formatted)
			}
			againDocument, err := ParseDocument(formatted)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Format(againDocument)
			if err != nil || !bytes.Equal(formatted, again) {
				t.Fatalf("format changed on second pass: %v", err)
			}
		})
	}
}
