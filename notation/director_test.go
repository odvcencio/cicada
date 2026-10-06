package notation

import (
	"os"
	"strings"
	"testing"
)

func TestDirectorParseFormatAndDiagnostics(t *testing.T) {
	source, err := os.ReadFile("../examples/game-director.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := Parse(source)
	if len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	if len(score.Live.States) != 2 || len(score.Live.Stingers) != 1 || len(score.Live.Transitions) != 2 {
		t.Fatalf("live: %+v", score.Live)
	}
	document, err := ParseDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	again, ds := Parse(formatted)
	if len(ds) != 0 {
		t.Fatalf("formatted: %+v\n%s", ds, formatted)
	}
	if again.Live.Stingers[0].CrossfadeMS != 10 || again.Live.Transitions[1].Quantize != "phrase" {
		t.Fatalf("round trip: %+v", again.Live)
	}
	for _, tc := range []struct{ old, new string }{
		{"state explore = calm", "state explore = missing"},
		{"cue.hit", "missing.hit"},
		{"cue.hit", "cue.missing"},
		{"quantize beat", "quantize pattern"},
		{"crossfade 10ms", "crossfade -1ms"},
		{"crossfade 10ms", "crossfade 61s"},
		{"phrase = 8bars", "phrase = 0bars"},
		{"transition explore -> combat", "transition missing -> combat"},
		{"cue = off", "cue = hit"},
		{"state combat = fight", "state explore = fight"},
	} {
		t.Run(tc.new, func(t *testing.T) {
			_, ds := Parse([]byte(strings.Replace(string(source), tc.old, tc.new, 1)))
			for _, d := range ds {
				if d.Severity == "error" {
					return
				}
			}
			t.Fatalf("accepted invalid director: %s", tc.new)
		})
	}
}
