package smf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func manyTrackProject(t *testing.T, tracks int, expressiveLast bool) *project.Project {
	t.Helper()
	var source strings.Builder
	source.WriteString("cicada 2\ntempo 120\ninstrument tone { voice mono { out = sine(pitch) * env(gate, 200ms) } }\n")
	source.WriteString("pattern hit notes { c4 . . . }\npattern bent performance length=3840ticks { note 60 at=0ticks length=960ticks velocity=100 { gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5; gesture at=480ticks bend=100ct pressure=0.5 timbre=0.5; } }\narrange {\n")
	for i := 0; i < tracks; i++ {
		pattern := "hit"
		if expressiveLast && i == tracks-1 {
			pattern = "bent"
		}
		fmt.Fprintf(&source, "  place p%d t%d %s { at = @1.1.1 length = 1bars }\n", i, i, pattern)
	}
	source.WriteString("}\n")
	for i := 0; i < tracks; i++ {
		fmt.Fprintf(&source, "track t%d tone {}\n", i)
	}
	score, ds := notation.Parse([]byte(source.String()))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture %+v %+v\n%s", ds, more, source.String())
	}
	return p
}

// A session with more melodic tracks than MIDI channels still exports: each
// track keeps its own chunk and later tracks share channels in turn.
func TestMoreThanFifteenMelodicTracksShareChannels(t *testing.T) {
	file, err := FromProject(manyTrackProject(t, 18, false), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Tracks) != 19 {
		t.Fatalf("got %d chunks, want 19", len(file.Tracks))
	}
	for i := 1; i < len(file.Tracks); i++ {
		if len(file.Tracks[i].Notes) == 0 || file.Tracks[i].Name != fmt.Sprintf("t%d", i-1) {
			t.Fatalf("track %d lost its notes or name: %+v", i, file.Tracks[i])
		}
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(&encoded); err != nil {
		t.Fatal(err)
	}
}

// Note expression is channel-wide in MIDI 1.0, so a shared channel refuses it.
func TestSharedChannelRefusesNoteExpression(t *testing.T) {
	if _, err := FromProject(manyTrackProject(t, 16, true), 1); err == nil || !strings.Contains(err.Error(), "note expression") {
		t.Fatalf("shared channel accepted a bending track: %v", err)
	}
	if _, err := FromProject(manyTrackProject(t, 15, true), 1); err != nil {
		t.Fatalf("fifteen tracks fit their own channels: %v", err)
	}
}
