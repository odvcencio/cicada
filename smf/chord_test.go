package smf

import (
	"bytes"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"testing"
)

func TestChordMIDIExportsEveryPitchAndSharedTies(t *testing.T) {
	source := `tempo 120 key d minor
 instrument piano { voice poly { out=sine(pitch)*env(gate,300ms) } }
 track keys piano {}
 pattern chord notes { [d4 f4 a4]^ - . . }
 scene main { keys=chord }
 song { main }`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("source %+v", ds)
	}
	file, err := FromProject(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	notes := file.Tracks[1].Notes
	if len(notes) != 12 {
		t.Fatalf("got %d chord notes, want12", len(notes))
	}
	for i, note := range notes {
		if note.Note != []uint8{62, 65, 69}[i%3] || note.Tick != int64(i/3)*960 || note.Dur != 480 || note.Vel != 127 {
			t.Fatalf("partial/changing cohort %+v", note)
		}
	}
	var encoded bytes.Buffer
	if err := Encode(file, &encoded); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Tracks[1].Notes) != 12 {
		t.Fatal("MIDI wire lost pitches")
	}
}

// This records an inherited interchange limitation separately from pitch and
// timing coverage. It must not be interpreted as realized chance-event parity.
func TestKnownMIDIChanceIterationZeroMismatch(t *testing.T) {
	source := `tempo 120 key d minor seed 7 instrument piano { voice poly { out=sine(pitch)*env(gate,300ms) } } track keys piano {} pattern a notes { [d4 f4]?50 } scene verse { keys=a } song { verse }`
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("source %+v", ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	file, err := FromProject(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	clock, _ := seq.NewClock(48000, 120000)
	var events [128]seq.Event
	n, overflow := seq.EventsWithGatesInBlock(&cfg.Patterns[0].Slots[0], clock, 0, 0, 0, 96000, events[:])
	if overflow {
		t.Fatal("event overflow")
	}
	onsets := 0
	for _, event := range events[:n] {
		if event.Kind == seq.NoteOn {
			onsets++
		}
	}
	midiOnsets := len(file.Tracks[1].Notes) / 2
	t.Logf("KNOWN INHERITED LIMITATION: realized playback%d cohorts vs MIDI%d cohorts (export uses iteration0 on repeats)", onsets, midiOnsets)
	if onsets != 10 || midiOnsets != 16 {
		t.Fatalf("inherited chance behavior changed: playback%d MIDI%d; review separately", onsets, midiOnsets)
	}
}
