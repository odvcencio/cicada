package main

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPatternPlacementLengthUsesTheCompiledGrid(t *testing.T) {
	for _, check := range []struct {
		name, body, description string
		ticks                   int
		kind                    string
	}{
		{"sixteenths", "c4 . e4 .", "4 steps · sixteenth notes · 1 beat per loop", 960, "notes"},
		{"half notes", "step=1/2 c4 . e4 .", "4 steps · half notes · 2 bars per loop", 7680, "notes"},
		{"eighth triplets", "step=1/8t c4 . e4 .", "4 steps · eighth-note triplets · 4/3 beats per loop", 1280, "notes"},
		{"sixteenth triplets", "step=1/16t c4 . e4 .", "4 steps · sixteenth-note triplets · 2/3 beat per loop", 640, "notes"},
		{"dotted eighths", "step=1/8. c4 . e4 .", "4 steps · dotted eighth notes · 3 beats per loop", 2880, "notes"},
		{"quintuplets", "step=1/20 c4 . e4 .", "4 steps · 1/5 beat per step · 4/5 beat per loop", 768, "notes"},
		{"dotted 128ths", "step=1/128. c4 . e4 .", "4 steps · dotted 128th notes · 3/16 beat per loop", 180, "notes"},
		{"chord and tie", "step=1/8 [c4 e4 g4] -", "2 steps · eighth notes · 1 beat per loop", 960, "notes"},
		{"tuplet group", "step=1/8 [1 3 5] -", "6 steps · sixteenth-note triplets · 1 beat per loop", 960, "acid"},
	} {
		t.Run(check.name, func(t *testing.T) {
			sound := "piano"
			if check.kind == "acid" {
				sound = "acid"
			}
			source := "cicada 2\ntrack keys " + sound + " {}\npattern fresh " + check.kind + " { " + check.body + " }\narrange { place opening keys fresh { at=@1.1.1 length=4bars } }\n"
			score, ds := notation.Parse([]byte(source))
			p, more := project.FromScore(score)
			if p == nil || len(ds) != 0 || len(more) != 0 {
				t.Fatal(ds, more)
			}
			address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p, Source: source})
			page := getPage(t, client, address+"/?panel=patterns&pattern=fresh")
			if !strings.Contains(page, fmt.Sprintf(`name="length" value="%dticks"`, check.ticks)) {
				t.Fatalf("placement does not use the authored %d-tick loop", check.ticks)
			}
			if !strings.Contains(page, check.description) || !strings.Contains(page, fmt.Sprintf(`data-pattern-loop-ticks="%d"`, check.ticks)) {
				t.Fatalf("editor misrepresents cells or loop: steps=%d stepTicks=%d; want %q", p.Patterns[0].Steps, p.Patterns[0].StepTicks, check.description)
			}
		})
	}
}
