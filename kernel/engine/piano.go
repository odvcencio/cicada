package engine

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/piano"
)

func validPianoPattern(pattern seq.Pattern) bool {
	for i := uint8(0); i < pattern.Len; i++ {
		step, err := seq.UnpackStep(pattern.Steps[i])
		if err != nil {
			return false
		}
		note := int(step.Note) + int(pattern.Transpose)
		if step.Gate && !step.Tie && (note < piano.MinNote || note > piano.MaxNote) {
			return false
		}
	}
	return true
}
