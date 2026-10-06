package transcription

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func measuredNote(start, end, midi float64) Note {
	return Note{Start: start, End: end, MIDI: midi, PitchHz: 440 * math.Exp2((midi-69)/12), Cents: 100 * (midi - math.Round(midi)), Confidence: .98, Velocity: 100}
}

func TestScoreRowsPreserveKeyChromaticPitchAndDetuning(t *testing.T) {
	o := DefaultOptions()
	o.Tempo, o.Key = 120, "d minor"
	r := Result{Notes: []Note{measuredNote(0, .25, 62.23), measuredNote(.25, .5, 66), measuredNote(.5, .75, 96)}}
	r.Notes[0].Pitch = []PitchPoint{{Time: 0, Cents: 23}, {Time: .125, Cents: 31}}
	r, err := Complete(r, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Key != "d minor" || r.Tempo != 120 || !strings.Contains(r.Source, "bend:") {
		t.Fatal(r)
	}
	score, ds := notation.Parse([]byte(r.Source))
	if err := diagnosticError(ds); err != nil {
		t.Fatal(err)
	}
	p, ds := project.FromScore(score)
	if err := diagnosticError(ds); err != nil {
		t.Fatal(err)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	pattern := cfg.Patterns[0].Slots[0]
	for i, want := range []int{62, 66, 96} {
		step, err := seq.UnpackStep(pattern.Steps[i*2])
		if err != nil || int(step.Note) != want {
			t.Fatalf("step %d: %v %v, want %d", i, step, err, want)
		}
	}
	if math.Abs(float64(pattern.ExpressionAt(0).PitchCents)-23) > .01 || math.Abs(float64(pattern.ExpressionAt(1).PitchCents)-31) > .01 {
		t.Fatal("pitch movement was discarded")
	}
	doc, _ := notation.ParseDocument([]byte(r.Source))
	formatted, err := notation.Format(doc)
	if err != nil || string(formatted) != r.Source {
		t.Fatal("generated source is not format-stable", err)
	}
}

func TestDegreePitchesCompileAcrossKeysAndRange(t *testing.T) {
	for root := range 12 {
		for mode := range 2 {
			key := pitchClasses[root] + " " + []string{"major", "minor"}[mode]
			for midi := 29; midi <= 96; midi++ {
				score := &notation.Score{KeyRoot: pitchClasses[root], Scale: []string{"major", "minor"}[mode]}
				pattern := notation.Pattern{Name: "take", Kind: "notes", Steps: []notation.StepToken{{Text: degreePitch(midi, root, mode)}}}
				track := notation.Track{Name: "melody", Kind: "acid", Params: []notation.Param{{Name: "octave", Value: "4"}}}
				compiled, err := project.CompilePattern(score, pattern, track)
				if err != nil {
					t.Fatal(key, midi, err)
				}
				step, _ := seq.UnpackStep(compiled[0].Pattern.Steps[0])
				if int(step.Note) != midi {
					t.Fatalf("%s note %d spells %s but compiles to %d", key, midi, pattern.Steps[0].Text, step.Note)
				}
			}
		}
	}
}

func TestScoreQuantizationAndCollisions(t *testing.T) {
	o := DefaultOptions()
	o.Tempo, o.Key = 120, "c major"
	r, err := Complete(Result{Notes: []Note{measuredNote(.018, .26, 60)}}, o)
	if err != nil || math.Abs(r.QuantizationMS-14) > .001 {
		t.Fatal("wrong boundary movement", r.QuantizationMS, err)
	}
	_, err = Complete(Result{Notes: []Note{measuredNote(.01, .04, 60), measuredNote(.04, .06, 62)}}, o)
	if err == nil {
		t.Fatal("collapsed onsets must not erase a note")
	}
	o.Strict = 0
	if _, err := Complete(Result{Notes: []Note{measuredNote(0, .25, 60)}}, o); err == nil {
		t.Fatal("unsupported expressive timing must be explicit")
	}
}

func TestContextInferenceConfidenceAndMeter(t *testing.T) {
	var notes []Note
	for i := range 24 {
		n := measuredNote(float64(i)*.5, float64(i)*.5+.3, float64([]int{60, 64, 67}[i%3]))
		n.Velocity = 70
		if i%3 == 0 {
			n.Velocity = 120
		}
		notes = append(notes, n)
	}
	bpm, _ := inferTempo(notes)
	if math.Abs(bpm-120) > .5 {
		t.Fatal("incorrect beat estimate", bpm)
	}
	meter, confidence := inferMeter(notes, 120)
	if meter != "3/4" || confidence <= 0 {
		t.Fatal("recurring accents were not detected", meter, confidence)
	}
	if _, confidence := inferTempo(notes[:2]); confidence != 0 {
		t.Fatal("sparse notes cannot determine tempo")
	}
	if _, _, err := parseKey("a# minor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseKey("x minor"); err == nil {
		t.Fatal("invalid key accepted")
	}
}
