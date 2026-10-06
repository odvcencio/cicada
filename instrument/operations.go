package instrument

// OperationInfo is shared by editor hover and cicada explain graph.<operation>.
type OperationInfo struct {
	Name      string `json:"name"`
	Signature string `json:"signature"`
	Meaning   string `json:"meaning"`
}

func Operation(name string) (OperationInfo, bool) {
	switch name {
	case "ddsp":
		return OperationInfo{name, "ddsp(hz, unit) -> audio", "Pinned CC0 quantized neural reed: eight harmonics plus filtered noise, controlled by fundamental frequency and linear loudness. Trained on synthetic 80..1600 Hz tones. Controls update every 64 frames; integer inference is allocation-free."}, true
	case "delay":
		return OperationInfo{name, "delay(audio, ms) -> audio", "Linear fractional delay; 1..4096 samples at the render rate. Reserves 16384 bytes per voice. Use 1 / pitch for a period in ms."}, true
	case "comb":
		return OperationInfo{name, "comb(audio, ms, unit, unit) -> audio", "Feedback comb with phase-tuned allpass interpolation and one-pole damping. Time is the complete loop period (4..4096 samples); feedback and damping are 0 inclusive to 1 exclusive. Reserves 16384 bytes per voice. Use 1 / pitch for a tuned pluck."}, true
	}
	return OperationInfo{}, false
}
