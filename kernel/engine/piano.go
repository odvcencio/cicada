package engine

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/piano"
)

func validPianoPattern(pattern *seq.Pattern) bool {
	for i := uint8(0); i < pattern.Len; i++ {
		step, err := seq.UnpackStep(pattern.Steps[i])
		if err != nil {
			return false
		}
		note := int(step.Note) + int(pattern.Transpose)
		if step.Gate && !step.Tie && (note < piano.MinNote || note > piano.MaxNote) {
			return false
		}
		chord := pattern.Chords[i]
		for n := uint8(0); n < chord.Count; n++ {
			note := int(chord.Notes[n]) + int(pattern.Transpose)
			if note < piano.MinNote || note > piano.MaxNote {
				return false
			}
		}
	}
	return true
}

func (e *Engine) releasePianoPattern(track int) {
	p := &e.patterns[track]
	for n := uint8(0); n < p.playingPitchCount; n++ {
		e.voices[track].piano.NoteOff(p.playingPitches[n])
	}
	p.playingPitchCount = 0
}
