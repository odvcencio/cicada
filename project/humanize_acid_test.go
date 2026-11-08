package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// The acid voice ignores velocity, so a humanized pattern on an acid track is
// refused rather than stored and silently ignored.
func TestAcidTrackRefusesHumanize(t *testing.T) {
	for _, kind := range []string{"", " notes"} {
		source := "cicada 2\ntempo 120\ntrack lead acid {}\npattern line" + kind + " humanize = 32 { 1 . 3 . }\nscene main { lead = line }\nsong { main }\n"
		score, ds := notation.Parse([]byte(source))
		if score == nil {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := FromScore(score)
		if p != nil {
			t.Fatalf("pattern kind %q: humanize on an acid track was accepted", kind)
		}
		found := false
		for _, d := range ds {
			found = found || strings.Contains(d.Message, "humanize")
		}
		if !found {
			t.Fatalf("pattern kind %q: no humanize diagnostic in %+v", kind, ds)
		}
	}
}
