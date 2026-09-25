package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/notation"
)

func TestLegacyDrumLabelSpacingPreservesSemantics(t *testing.T) {
	const source = "cicada 1\ntrack drums drums {}\npattern beat drums steps=8 { bd: Xx7x*2x?50....; sd: ....X...; ch: xxxxxxxx; }\nscene main { drums=beat }\nsong { main }\n"
	parse := func(t *testing.T, source string) *Project {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		if len(diagnostics) != 0 {
			t.Fatalf("parse diagnostics: %+v", diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("project diagnostics: %+v", diagnostics)
		}
		if _, err := CompileEngine(p, 48000, 128); err != nil {
			t.Fatal(err)
		}
		return p
	}
	want := parse(t, source)
	for _, gap := range []string{" ", "\t", "\r\n", " // keep label comment\n"} {
		t.Run(gap, func(t *testing.T) {
			input := strings.ReplaceAll(source, ":", gap+":")
			if !SemanticEqual(want, parse(t, input)) {
				t.Fatal("legacy label spacing changed the music")
			}
			doc, err := notation.ParseDocument([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := notation.Format(doc)
			if err != nil {
				t.Fatal(err)
			}
			if !SemanticEqual(want, parse(t, string(formatted))) {
				t.Fatal("formatting changed the music")
			}
			formattedDoc, err := notation.ParseDocument(formatted)
			if err != nil {
				t.Fatal(err)
			}
			again, err := notation.Format(formattedDoc)
			if err != nil || string(again) != string(formatted) {
				t.Fatalf("formatting is not idempotent: %v\n%s\n%s", err, formatted, again)
			}
			if strings.Contains(gap, "//") && strings.Count(string(formatted), "// keep label comment") != 3 {
				t.Fatal("formatting lost a label comment")
			}
			spans, err := language.Highlight([]byte(input))
			if err != nil {
				t.Fatal(err)
			}
			labels, comments := 0, 0
			for _, span := range spans {
				if span.Capture == "tag.builtin" {
					labels++
				}
				if span.Capture == "comment" {
					comments++
				}
			}
			if labels != 3 || strings.Contains(gap, "//") && comments != 3 {
				t.Fatalf("label/comment highlight counts: %d/%d", labels, comments)
			}
		})
	}
	if !SemanticEqual(want, parse(t, strings.ReplaceAll(source, ";", ""))) {
		t.Fatal("rows without semicolons changed the music")
	}
}
