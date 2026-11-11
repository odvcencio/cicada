package smf_test

import (
	"fmt"
	"io"
	"slices"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
	"m31labs.dev/cicada/smf"
)

// parityVoices is baobab's lab/phase.cicada with a drum track added: a
// chance pattern, a humanized pattern with accents, and a drum pattern with
// chance, humanize and accented hits.
const parityVoices = `cicada 2
tempo 120
seed 9
instrument click {
  voice mono {
    out = sine(pitch) * env(gate, 30ms) * velocity * 0.3
  }
}
track a click {}
track b click {}
track d drums {}
pattern ch { c5?50 c5?50 c5?50 c5?50 }
pattern hum { humanize = 20 c5 c5^ c5 c5^ }
pattern beat drums {
  humanize = 12
  bd: X..x?60 x..X
  ch: x?50x.x x.x?30x
}
`

// TestMIDIExportPlaysWhatTheRendererPlays is baobab's finding: for chance
// (?N) the render drew a new mask on each pass while cicada midi repeated the
// first pass's, and humanize varied velocity in audio while MIDI wrote 100.
// The exported notes, their ticks and their velocities now equal the note
// events the renderer plays, on the scene renderer and the arrangement
// renderer.
func TestMIDIExportPlaysWhatTheRendererPlays(t *testing.T) {
	for name, tail := range map[string]string{
		"scenes":      "scene s1 { a = ch b = hum d = beat }\nsong { s1*4 }\n",
		"arrangement": "arrange {\n  place p1 a ch { at = @1.1.1 length = 4bars }\n  place p2 b hum { at = @1.2.1 length = 3bars }\n  place p3 d beat { at = @1.1.1 length = 4bars }\n}\n",
		// A tape echo sends a scene song to the scheduled engine.
		"scenes on the scheduled engine": "fx echo tape {}\nscene s1 { a = ch b = hum d = beat }\nsong { s1*4 }\n",
	} {
		t.Run(name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(parityVoices + tail))
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatal(d.Error())
				}
			}
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			scheduled := render.UsesScheduledEngine(score, render.Options{})
			if scheduled != (name != "scenes") {
				t.Fatalf("%s: scheduled engine %v", name, scheduled)
			}
			file, err := smf.FromProjectWith(p, 4, smf.Options{SceneRenderer: !scheduled})
			if err != nil {
				t.Fatal(err)
			}
			var exported []string
			for _, track := range file.Tracks[1:] {
				for _, note := range track.Notes {
					exported = append(exported, fmt.Sprintf("track %d tick %d note %d velocity %d", note.Track-1, note.Tick, note.Note, note.Vel))
				}
			}
			var played []string
			opts := render.Options{SampleRate: 48000, Bits: 32, Bars: 4, NoteTap: func(track int, event seq.Event) {
				if event.Kind != seq.NoteOn {
					return
				}
				// The scene renderer gives a drum hit its General MIDI note,
				// the scheduled engine its lane index.
				note := event.Note
				if track == 2 && scheduled {
					note = drum.MIDINotes[note]
				}
				// Playback plays an accent at 127 after humanizing it.
				velocity := event.Velocity
				if velocity == 0 {
					velocity = 100
				}
				if event.Accent {
					velocity = 127
				}
				played = append(played, fmt.Sprintf("track %d tick %d note %d velocity %d", track, event.Tick, note, velocity))
			}}
			if _, err := render.WAV(score, opts, io.Discard); err != nil {
				t.Fatal(err)
			}
			slices.Sort(exported)
			slices.Sort(played)
			if !slices.Equal(exported, played) {
				t.Fatalf("MIDI (%d notes) and playback (%d notes) differ\nonly in MIDI:     %v\nonly in playback: %v", len(exported), len(played), without(exported, played), without(played, exported))
			}
			// The score must exercise both draws: some chance steps skipped
			// on some passes and not others, and velocities other than 100.
			counts, velocities := map[string]int{}, map[string]bool{}
			for _, line := range played {
				var track, tick, note, velocity int
				fmt.Sscanf(line, "track %d tick %d note %d velocity %d", &track, &tick, &note, &velocity)
				counts[fmt.Sprint(track, tick%960)]++
				velocities[fmt.Sprint(velocity)] = true
			}
			if len(velocities) < 5 || counts["0 0"] == 0 || counts["0 0"] == 16 {
				t.Fatalf("the score does not exercise chance and humanize: %d velocities, track a beat-one hits %d of 16", len(velocities), counts["0 0"])
			}
		})
	}
}

// without lists the entries of a that b lacks.
func without(a, b []string) []string {
	var only []string
	for _, entry := range a {
		if !slices.Contains(b, entry) {
			only = append(only, entry)
		}
	}
	return only
}
