package engine

// AmpTargets is the currently commanded state of one track's unique amp.
// Continuous values are targets, before the amp's internal smoothing.
type AmpTargets struct {
	Drive, Tone, Output float64
	Cabinet             int
	Bypass              bool
}

// TrackAmpTargets reads render-owned state and may only be called by the audio
// thread, after its render call has completed. It rejects missing or ambiguous
// amps. It is not safe for a control thread to call concurrently with Render.
func (e *Engine) TrackAmpTargets(track int) (AmpTargets, bool) {
	if e == nil || track < 0 || track >= e.tracks {
		return AmpTargets{}, false
	}
	return e.voices[track].chain.monitorAmpTargets()
}
