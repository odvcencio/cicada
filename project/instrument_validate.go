package project

import (
	"fmt"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
)

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
