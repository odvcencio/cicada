package main

import (
	"bytes"
	"os"
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
	diagnostics, _ := portableFeatureRefusals(p)
	for _, d := range diagnostics {
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

// A score whose only portable problem is a tape echo reports it once: the
// image encoder's own refusal of the same feature is not printed again.
func TestBrowserCheckReportsAFeatureOnce(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/main.cicada"
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\ntrack lead acid { send echo = 0.2 }\npattern line acid { 1 . 3 . }\nscene main { lead = line }\nsong { main }\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := checkCommand([]string{"--profile", "browser", path}, &stdout, &stderr)
	if err == nil || err.Error() != "1 error(s) across 1 score(s)" || strings.Count(stderr.String(), "tape echo") != 1 {
		t.Fatalf("want the tape echo reported once: %v\n%s", err, stderr.String())
	}
}
