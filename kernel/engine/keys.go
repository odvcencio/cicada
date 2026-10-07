package engine

func (e *Engine) releaseKeysPattern(track int) {
	if keysEnabled {
		p := &e.patterns[track]
		for n := uint8(0); n < p.playingPitchCount; n++ {
			e.voices[track].keys.NoteOff(p.playingPitches[n])
		}
		p.playingPitchCount = 0
	}
}
