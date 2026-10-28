package render

import (
	"bytes"
	"math"
	"path/filepath"
	"slices"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Song scores render through the direct WAV mixer, which compiles patterns per
// track. A chord pattern is valid on the poly track it is bound to and invalid
// on a vibraphone, so the vibraphone track must compile only the patterns bound
// to it, by a scene or by a source chain.
const modalChordPatterns = `cicada 2
tempo 120
key c major
seed 7
instrument pad {
  voice poly {
    out = sine(pitch) * env(gate, 200ms) * velocity * 0.1
  }
}
pattern line notes { gate = 100% c4 . e4 . g4 . c5 . }
pattern answer notes { gate = 100% g4 . e4 . d4 . c4 . }
pattern comp notes { [c3 e3 g3] . . . [f3 a3 c4] . . . }
`

const (
	modalChordScenes = `track vibes model_vibraphone { octave = 4 }
track keys pad {}
scene verse { vibes = line keys = comp }
scene chorus { vibes = answer keys = comp }
song { verse chorus }
`
	modalVibesScenes = `track vibes model_vibraphone { octave = 4 }
scene verse { vibes = line }
scene chorus { vibes = answer }
song { verse chorus }
`
	modalChordChains = `track vibes model_vibraphone { octave = 4 chain = line answer }
track keys pad { chain = comp }
scene main {}
song { main*2 }
`
	modalVibesChains = `track vibes model_vibraphone { octave = 4 chain = line answer }
scene main {}
song { main*2 }
`
)

func TestModalTrackIgnoresChordPatternBoundToAnotherTrack(t *testing.T) {
	for _, tc := range []struct{ name, tracks, vibesOnly string }{
		{"scenes", modalChordScenes, modalVibesScenes},
		{"chains", modalChordChains, modalVibesChains},
	} {
		t.Run(tc.name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(modalChordPatterns + tc.tracks))
			if score == nil || hasAudioErrors(ds) {
				t.Fatalf("score diagnostics: %+v", ds)
			}
			if p, ds := project.FromScore(score); p == nil {
				t.Fatalf("check rejects the score: %+v", ds)
			}
			starts := map[int]int{}
			opts := Options{SampleRate: 48000, Bits: 32, NoteTap: func(track int, event seq.Event) {
				if event.Kind == seq.NoteOn {
					starts[track]++
				}
			}}
			var wav bytes.Buffer
			report, err := WAV(score, opts, &wav)
			if err != nil {
				t.Fatal(err)
			}
			// Two bars hold four passes of an 8-step pattern on each track. A
			// vibraphone pass has four notes and a chord pass has two chords.
			if starts[0] != 16 || starts[1] != 8 {
				t.Errorf("note starts by track = %v, want 16 vibraphone notes and 8 chords", starts)
			}
			assertSceneEngineMatchesWAV(t, score, wav.Bytes(), 48000, 0, int(report.Frames))

			// The vibraphone sounds the same with the chord track beside it as alone.
			alone, ds := notation.Parse([]byte(modalChordPatterns + tc.vibesOnly))
			if alone == nil || hasAudioErrors(ds) {
				t.Fatalf("vibraphone-only diagnostics: %+v", ds)
			}
			var stems [2]driftAudio
			for i, s := range []*notation.Score{score, alone} {
				dir := filepath.Join(t.TempDir(), "stems")
				if _, err := Stems(s, Options{SampleRate: 48000}, dir); err != nil {
					t.Fatal(err)
				}
				stems[i] = driftReadWAV(t, filepath.Join(dir, "01-vibes.wav"))
			}
			var peak float64
			for _, v := range stems[1].left {
				peak = max(peak, math.Abs(float64(v)))
			}
			t.Logf("vibraphone stem peak %.4f over %d frames", peak, len(stems[1].left))
			if peak < .001 {
				t.Fatalf("vibraphone stem is silent: peak %g", peak)
			}
			if !slices.Equal(stems[0].left, stems[1].left) || !slices.Equal(stems[0].right, stems[1].right) {
				t.Error("the chord track changed the vibraphone stem")
			}
		})
	}
}
