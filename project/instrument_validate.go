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
// graph's bounds at the playback or export rate. Rests and ties do not trigger
// a new pitch; transposition applies to each gated note.
func ValidateGraphDelayPattern(program graph.Program, sampleRate int, pattern seq.Pattern) error {
	if program.DelaySamples() == 0 {
		return nil
	}
	for i := 0; i < int(pattern.Len); i++ {
		step, _ := seq.UnpackStep(pattern.Steps[i])
		if !step.Gate || step.Tie {
			continue
		}
		notes, count := pattern.Chords[i].Notes, pattern.Chords[i].Count
		if count == 0 {
			notes[0], count = step.Note, 1
		}
		for _, note := range notes[:count] {
			if err := validateGraphDelayNote(program, sampleRate, int(note)+int(pattern.Transpose)); err != nil {
				return err
			}
		}
	}
	return nil
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
