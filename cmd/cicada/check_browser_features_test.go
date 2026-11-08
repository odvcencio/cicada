package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Every portable refusal is listed, not just the first the image hits.
func TestPortableFeatureRefusalsListsEveryBlocker(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\ntrack lead acid { send echo = 0.2 }\ntrack beat drums {}\n" +
		"pattern line acid { 1 . 3 . }\npattern hits drums humanize = 6 { bd: x... x... }\npattern spare drums humanize = 9 { bd: x... }\n" +
		"scene main { lead = line beat = hits }\nsong { main }\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	var messages []string
	for _, d := range portableFeatureRefusals(p) {
		messages = append(messages, d.Message)
	}
	text := strings.Join(messages, "\n")
	if len(messages) != 2 || !strings.Contains(text, "effect echo is a tape echo") || !strings.Contains(text, "pattern hits uses humanize = 6") {
		t.Fatalf("want the tape echo and the played humanize pattern, got:\n%s", text)
	}
	if strings.Contains(text, "spare") {
		t.Fatalf("an unplayed pattern was reported:\n%s", text)
	}
}
