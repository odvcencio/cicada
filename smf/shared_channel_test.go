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

// Note expression is channel-wide in MIDI 1.0, so a bending track that shares a
// channel moves to its own port; the track it shares with stays untouched.
func TestSharedChannelKeepsNoteExpressionOnItsOwnPort(t *testing.T) {
	file, err := FromProject(manyTrackProject(t, 16, true), 1)
	if err != nil {
		t.Fatal(err)
	}
	port := func(track *TrackChunk) byte {
		for _, meta := range track.Meta {
			if meta.Type == 0x21 && len(meta.Data) == 1 {
				return meta.Data[0]
			}
		}
		return 0
	}
	var plain, bent *TrackChunk
	for i := range file.Tracks {
		switch track := &file.Tracks[i]; {
		case track.Name == "t0":
			plain = track
		case track.Name == "t15" && len(track.Notes) > 0:
			bent = track
		}
	}
	if plain == nil || bent == nil {
		t.Fatalf("missing tracks: t0 %v, t15 %v", plain != nil, bent != nil)
	}
	if port(plain) == port(bent) {
		t.Fatalf("bending track shares port %d with t0", port(plain))
	}
	if len(plain.Events) != 0 || len(bent.Events) == 0 {
		t.Fatalf("channel events: t0 has %d, t15 has %d", len(plain.Events), len(bent.Events))
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
}
