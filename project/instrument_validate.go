package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func validateGraphDelayNote(program graph.Program, sampleRate, note int) error {
	pitch := float32(440 * math.Exp2(float64(note-69)/12))
	return graph.ValidateDelayPitch(program, sampleRate, pitch)
}

// ValidateGraphDelayPattern rejects sequenced delay periods that exceed the
// graph's bounds at the playback or export rate. Ties keep the preceding note
// while their expression can change its pitch. Vibrato checks both extrema.
func ValidateGraphDelayPattern(program graph.Program, sampleRate int, pattern seq.Pattern) error {
	if program.DelaySamples() == 0 {
		return nil
	}
	var notes [4]uint8
	count := 0
	for i := 0; i < int(pattern.Len); i++ {
		step, _ := seq.UnpackStep(pattern.Steps[i])
		if !step.Gate {
			count = 0
			continue
		}
		if !step.Tie {
			chord := pattern.Chords[i]
			notes, count = chord.Notes, int(chord.Count)
			if count == 0 {
				notes[0], count = step.Note, 1
			}
		}
		bend, depth := float32(0), float32(0)
		if expression := pattern.ExpressionAt(i); expression.Set {
			bend = expression.PitchCents
			if expression.VibratoRateHz != 0 {
				depth = expression.VibratoDepthCents
			}
		}
		for _, note := range notes[:count] {
			for _, cents := range [...]float32{bend - depth, bend + depth} {
				pitch := float32(440 * math.Exp2(float64(int(note)+int(pattern.Transpose)-69)/12+float64(cents)/1200))
				if err := graph.ValidateDelayPitch(program, sampleRate, pitch); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateProjectGraphDelayPattern(program graph.Program, sampleRate int, source Pattern) error {
	pattern, err := kernelPattern(source)
	if err != nil {
		return err
	}
	for i, step := range source.Data {
		if step != nil && len(step.Notes) > 0 {
			pattern.Chords[i].Count = uint8(len(step.Notes))
			for j, note := range step.Notes {
				pattern.Chords[i].Notes[j] = uint8(note)
			}
		}
		pattern.Steps[i], err = packProjectStep(step, false, 0)
		if err != nil {
			return err
		}
	}
	return ValidateGraphDelayPattern(program, sampleRate, pattern)
}

// compileInstrument uses the same notation and DSP compilers as source input,
// without requiring unrelated project fields to round-trip through source v1.
func compileInstrument(inst Instrument) (*instrument.Program, error) {
	decl, err := instrumentSource(inst)
	if err != nil {
		return nil, err
	}
	source := "cicada 1\n" + decl +
		"\ntrack __check " + inst.ID + " {}\n" +
		"pattern __check_pattern notes steps=1 { 1 }\n" +
		"scene __check_scene { __check=__check_pattern }\n" +
		"song { __check_scene }\n"
	score, diagnostics := notation.Parse([]byte(source))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return nil, fmt.Errorf("%s", diagnostic.Message)
		}
	}
	if score == nil || len(score.Instruments) != 1 {
		return nil, fmt.Errorf("instrument declaration could not be parsed")
	}
	program, diagnostics := instrument.Compile(score.Instruments[0])
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return nil, fmt.Errorf("%s", diagnostic.Message)
		}
	}
	if program == nil {
		return nil, fmt.Errorf("instrument declaration could not be compiled")
	}
	if _, err := instrument.Lower(program, nil); err != nil {
		return nil, err
	}
	return program, nil
}
