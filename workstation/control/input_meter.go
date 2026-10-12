package control

import (
	"fmt"
	"math"
)

type InputMeterState struct {
	Enabled   bool    `json:"enabled"`
	Active    bool    `json:"active"`
	Channels  int     `json:"channels"`
	PeakL     float64 `json:"peakL"`
	PeakR     float64 `json:"peakR"`
	HeldPeakL float64 `json:"heldPeakL"`
	HeldPeakR float64 `json:"heldPeakR"`
	Clipped   bool    `json:"clipped"`
	Session   uint64  `json:"session"`
	Error     string  `json:"error"`
}

// MeterDB maps silence and invalid device values to the visible meter floor.
// Peak text retains positive dB values so overloads cannot appear as 0 dB.
func MeterDB(peak float64) float64 {
	if peak <= 0 || math.IsNaN(peak) || math.IsInf(peak, 0) {
		return -60
	}
	return math.Max(-60, 20*math.Log10(peak))
}

func MeterText(peak float64) string {
	if peak <= 0 || math.IsNaN(peak) || math.IsInf(peak, 0) {
		return "−∞ dBFS"
	}
	db := 20 * math.Log10(peak)
	if math.Abs(db) < .05 {
		db = 0
	}
	return fmt.Sprintf("%.1f dBFS", db)
}

func (m InputMeterState) Status() (string, string) {
	if m.Error != "" {
		return "Input needs attention", "Check your microphone in Recording setup."
	}
	if !m.Enabled {
		return "Microphone off", "Enable your microphone in Recording setup to check its level."
	}
	if m.Clipped {
		return "Clipped input", "Input reached its digital limit. Lower your microphone volume or move farther away before the next take."
	}
	if !m.Active {
		return "Ready to check input", "Play the song and sing to check your level, or start a take."
	}
	if max(m.PeakL, m.PeakR) == 0 {
		return "Listening for input", "Sing a phrase. If the meter stays empty, check the selected microphone and its input volume."
	}
	return "Input active", "Aim for peaks around −18 to −6 dBFS. Adjust your microphone volume; monitoring gain only changes what you hear."
}
