package instrument

// OperationInfo is shared by editor hover and cicada explain graph.<operation>.
type OperationInfo struct {
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Meaning   string `json:"meaning"`
}

func Operation(name string) (OperationInfo, bool) {
	switch name {
	case "neural_amp":
		return OperationInfo{name, "neural_amp(audio, drive) -> audio", "Pinned CC0 causal neural amp with integer inference. Drive is clamped to 0..8; eight samples of history, 32 bytes per node. Designed for 48 kHz; other render rates preserve deterministic sample-domain behavior."}, true
	case "pm":
		return OperationInfo{name, "pm(hz, audio, unit) -> audio", "Sine carrier with phase offset modulator * index in radians; index may be enveloped or negative. Carrier phase stays in [0,1), frequency clamps to 0..0.49 * sample_rate, and zero frequency emits silence. No feedback or antialiasing: high pitch, ratio, or index folds sidebands above Nyquist. Experimental pending listening acceptance."}, true
	case "delay":
		return OperationInfo{name, "delay(audio, ms) -> audio", "Linear fractional delay; 1..4096 samples at the render rate. Reserves 16384 bytes per voice. Use 1 / pitch for a period in ms."}, true
	case "comb":
		return OperationInfo{name, "comb(audio, ms, unit, unit) -> audio", "Feedback comb with phase-tuned allpass interpolation and one-pole damping. Time is the complete loop period (4..4096 samples); feedback and damping are 0 inclusive to 1 exclusive. Reserves 16384 bytes per voice. Use 1 / pitch for a tuned pluck."}, true
	}
	return OperationInfo{}, false
}
