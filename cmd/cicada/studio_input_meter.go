package main

import (
	"math"
	"sync/atomic"
)

// Only atomic scalars cross the callback boundary. Holding the maximum keeps
// a short overload visible even when it falls between browser polls.
var studioInputMeterSession atomic.Uint64

func (a *backendStudioAudio) resetInputMeter() {
	a.inputPeakL.Store(0)
	a.inputPeakR.Store(0)
	a.inputHeldL.Store(0)
	a.inputHeldR.Store(0)
	a.inputSession.Store(studioInputMeterSession.Add(1))
}

func inputMeterPeak(v float32) float32 {
	if v <= 0 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
		return 0
	}
	return v
}

func holdInputPeak(value *atomic.Uint32, peak float32) {
	bits := math.Float32bits(inputMeterPeak(peak))
	for old := value.Load(); bits > old; old = value.Load() {
		if value.CompareAndSwap(old, bits) {
			return
		}
	}
}

func (a *backendStudioAudio) measureInput(input [][]float32) {
	l, r, _, _ := measureStudioAudioInput(input)
	l, r = inputMeterPeak(l), inputMeterPeak(r)
	a.inputPeakL.Store(math.Float32bits(l))
	a.inputPeakR.Store(math.Float32bits(r))
	holdInputPeak(&a.inputHeldL, l)
	holdInputPeak(&a.inputHeldR, r)
}

type studioInputMeter struct {
	Enabled   bool    `json:"enabled"`
	Active    bool    `json:"active"`
	Channels  int     `json:"channels"`
	PeakL     float32 `json:"peakL"`
	PeakR     float32 `json:"peakR"`
	HeldPeakL float32 `json:"heldPeakL"`
	HeldPeakR float32 `json:"heldPeakR"`
	Clipped   bool    `json:"clipped"`
	Session   uint64  `json:"session"`
	Error     string  `json:"error,omitempty"`
}

// This projection neither enumerates devices nor opens an input stream.
func (t *studioTransport) inputMeter() studioInputMeter {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := studioInputMeter{Enabled: t.audioOptions.InputEnabled}
	r := t.lastInput
	if t.audio != nil {
		r = t.audio.Snapshot()
	}
	m.Channels, m.Session, m.Error = r.InputChannels, r.InputSession, r.Error
	m.Active = t.audio != nil && m.Enabled && m.Channels > 0 && (t.playing || r.Armed)
	m.PeakL, m.PeakR = inputMeterPeak(r.InputPeakL), inputMeterPeak(r.InputPeakR)
	m.HeldPeakL, m.HeldPeakR = inputMeterPeak(r.InputHeldL), inputMeterPeak(r.InputHeldR)
	// A full-scale 16-bit input is 32767/32768, slightly below 1.0.
	m.Clipped = max(m.HeldPeakL, m.HeldPeakR) >= .9999
	return m
}
