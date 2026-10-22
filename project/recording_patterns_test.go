package project

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestRecordingChoicesKeepBoundOrderAndOfferUnusedCompatiblePatterns(t *testing.T) {
	source := []byte("cicada 2\ntrack bass acid {}\ntrack keys piano {}\ntrack drums drums {}\npattern bass-part acid { c2 . }\npattern keys-part notes { c4 . }\npattern fresh notes { . . . . }\npattern low acid { c2 . }\npattern beat drums { bd: .... }\nscene main { bass=bass-part keys=keys-part }\nsong { main }\n")
	score, ds := notation.Parse(source)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	// Imported patterns have qualified names and must not become unused local destinations.
	foreign := p.Patterns[2]
	foreign.ID = "library.remote"
	p.Patterns = append(p.Patterns, foreign)
	got := RecordingPatternChoices(p)
	want := map[string][]string{"bass": {"bass-part", "fresh", "low"}, "keys": {"keys-part", "fresh"}, "drums": {"beat"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("choices=%v, want %v", got, want)
	}
	plan := EditPlan(p, source)
	for _, track := range plan.Tracks {
		if !reflect.DeepEqual(track.RecordingPatterns, want[track.ID]) {
			t.Fatal("domain destinations differ from picker", track)
		}
	}
}
