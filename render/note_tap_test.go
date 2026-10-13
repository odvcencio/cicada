package render

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

const noteTapVoice = `cicada 2
tempo 120
key c major
instrument pad {
  voice poly {
    out = saw(pitch) * env(gate, 200ms) * velocity * 0.2
  }
}
track keys pad {}
pattern chords notes {
  [c4 e4 g4] - . . [f4 a4 c5] . . .
}
`

// The tap sees the same chord notes on the scene renderer and on the
// arrangement (engine) renderer, and does not change the audio.
func TestNoteTapBothRenderPaths(t *testing.T) {
	sources := map[string]string{
		"scene":       noteTapVoice + "scene s { keys = chords }\nsong { s*2 }\n",
		"arrangement": noteTapVoice + "arrange { place p keys chords { at = @1.1.1 length = 2bars } }\n",
	}
	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(source))
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatal(d.Error())
				}
			}
			var plain bytes.Buffer
			if _, err := WAV(score, Options{Bits: 32, TailSec: 0.1}, &plain); err != nil {
				t.Fatal(err)
			}
			var starts, releases int
			var first seq.Event
			var tapped bytes.Buffer
			opts := Options{Bits: 32, TailSec: 0.1, NoteTap: func(track int, ev seq.Event) {
				if track != 0 {
					t.Errorf("track %d", track)
				}
				switch ev.Kind {
				case seq.NoteOn:
					if starts == 0 {
						first = ev
					}
					starts++
				case seq.NoteOff:
					releases++
				}
			}}
			if _, err := WAV(score, opts, &tapped); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(plain.Bytes(), tapped.Bytes()) {
				t.Error("the tap changed the rendered audio")
			}
			if starts != 8 || releases == 0 {
				t.Errorf("%d starts, %d releases, want 8 starts (two chords, four loops)", starts, releases)
			}
			if first.NoteCount != 3 || first.Notes[0]%12 != 0 || first.Notes[1]%12 != 4 || first.Notes[2]%12 != 7 {
				t.Errorf("first chord %+v", first)
			}
		})
	}
}
