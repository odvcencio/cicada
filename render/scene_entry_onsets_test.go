package render

import (
	"fmt"
	"io"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// sceneEntryOnsets renders a score and returns, per track, the ticks of its
// note starts. scheduled forces the engine path that sample packs, serial
// inserts and arrangements take; otherwise the score must suit the direct mixer.
func sceneEntryOnsets(t *testing.T, source string, scheduled bool) map[int][]int64 {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	onsets := map[int][]int64{}
	opts := Options{Bits: 32, NoteTap: func(track int, ev seq.Event) {
		if ev.Kind == seq.NoteOn {
			onsets[track] = append(onsets[track], ev.Tick)
		}
	}}
	var err error
	if scheduled {
		_, err = renderScheduleWAV(score, opts, io.Discard, "", 1)
	} else {
		if UsesScheduledEngine(score, opts) {
			t.Fatal("score does not suit the direct mixer")
		}
		_, err = WAV(score, opts, io.Discard)
	}
	if err != nil {
		t.Fatal(err)
	}
	return onsets
}

// idleBars lists the onsets of the idle pattern, one c3 per quarter note.
func idleBars(bars int64) []int64 {
	var ticks []int64
	for tick := int64(0); tick < bars*seq.TicksPerBar; tick += 960 {
		ticks = append(ticks, tick)
	}
	return ticks
}

const sceneEntryVoice = `cicada 2
tempo 120
instrument m { voice poly { out = sine(pitch) * adsr(gate, 5ms, 100ms, 0.8, 100ms) * 0.3 } }
track lead m {}
pattern idle { c3 . . . }
`

// A scene that binds a pattern at a bar where the pattern's previous pass
// would release a note (a tie or a full gate into the bar line) must still
// start the pattern's first note there. The scheduled renderer used to rebuild
// the track's events after the frame's release pass, so that release stopped
// the note pass and the first note of the pass was dropped (in Studio as well).
// Both render paths must start the same notes at the same ticks.
func TestSceneEntryStartsTheFirstNoteOnBothRenderPaths(t *testing.T) {
	const bar = seq.TicksPerBar
	cases := []struct {
		name, patterns, song string
		want                 []int64 // onsets of track lead
	}{
		{
			name:     "whole-bar tie bound at bar 3",
			patterns: "pattern one { step = 1/4 c4 - - - }\nscene x { lead = idle }\nscene y { lead = one }\n",
			song:     "song { x*2 y*3 }",
			want:     append(idleBars(2), 2*bar, 3*bar, 4*bar),
		},
		{
			name:     "half-bar tie bound at bar 3",
			patterns: "pattern half { step = 1/2 c4 - }\nscene x { lead = idle }\nscene y { lead = half }\n",
			song:     "song { x*2 y*2 }",
			want:     append(idleBars(2), 2*bar, 3*bar),
		},
		{
			name:     "full gate into the bar line",
			patterns: "pattern held { step = 1/4 gate = 100% c4 . . d4 }\nscene x { lead = idle }\nscene y { lead = held }\n",
			song:     "song { x*2 y*2 }",
			want:     append(idleBars(2), 2*bar, 2*bar+2880, 3*bar, 3*bar+2880),
		},
		{
			name:     "pattern to pattern",
			patterns: "pattern one { step = 1/4 c4 - - - }\npattern two { step = 1/4 e4 - - - }\nscene x { lead = one }\nscene y { lead = two }\n",
			song:     "song { x*2 y*2 }",
			want:     []int64{0, bar, 2 * bar, 3 * bar},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := sceneEntryVoice + c.patterns + c.song + "\n"
			direct := sceneEntryOnsets(t, source, false)
			scheduled := sceneEntryOnsets(t, source, true)
			if !reflect.DeepEqual(direct, scheduled) {
				t.Errorf("onsets differ between render paths:\ndirect    %v\nscheduled %v", direct, scheduled)
			}
			if fmt.Sprint(scheduled[0]) != fmt.Sprint(c.want) {
				t.Errorf("scheduled onsets %v, want %v", scheduled[0], c.want)
			}
		})
	}
}
