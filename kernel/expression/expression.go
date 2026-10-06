// Package expression defines normalized per-note controls shared by voices and
// score patterns. Controller updates and vibrato require no allocation.
package expression

import (
	"math"

	"m31labs.dev/cicada/kernel/dsp/fastmath"
)

// Params is one resolved score-step expression. Unset controls use zero pitch
// bend, zero pressure, centered timbre (0.5), and no vibrato. Pitch and vibrato
// depth are cents; rate is Hz. Set distinguishes an authored reset from absence.
type Params struct {
	PitchCents        float32
	Pressure          float32
	Timbre            float32
	VibratoRateHz     float32
	VibratoDepthCents float32
	Set               bool
}

type Error string

func (e Error) Error() string { return string(e) }

func (p Params) Validate() error {
	values := [...]float32{p.PitchCents, p.Pressure, p.Timbre, p.VibratoRateHz, p.VibratoDepthCents}
	for _, value := range values {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return Error("note expression must be finite")
		}
	}
	if p.PitchCents < -9600 || p.PitchCents > 9600 || p.Pressure < 0 || p.Pressure > 1 || p.Timbre < 0 || p.Timbre > 1 || p.VibratoRateHz < 0 || p.VibratoRateHz > 100 || p.VibratoDepthCents < 0 || p.VibratoDepthCents > 9600 {
		return Error("note expression is out of range")
	}
	return nil
}

// State keeps vibrato phase across tied-step updates. Reset starts a fresh note
// at zero phase; rate changes preserve phase, and zero depth bypasses vibrato.
type State struct {
	Params
	phase, increment float64
	ratio            float64
}

func (s *State) Reset() { *s = State{Params: Params{Timbre: .5}, ratio: 1} }

func (s *State) SetParams(p Params, rate float32) {
	if !p.Set {
		p = Params{Timbre: .5}
	}
	s.Params = p
	s.updateRatio()
	s.increment = float64(p.VibratoRateHz) / float64(rate)
}

// NextCents uses the same scalar formulas and evaluation order on native and
// WASM. It leaves the legacy pitch path untouched when expression is neutral.
func (s *State) NextCents() float64 {
	cents := float64(s.PitchCents)
	if s.VibratoDepthCents != 0 && s.increment != 0 {
		cents += float64(s.VibratoDepthCents) * math.Sin(2*math.Pi*s.phase)
		s.phase += s.increment
		if s.phase >= 1 {
			s.phase--
		}
	}
	return cents
}

// SetControls retains a running vibrato while updating live note controllers.
func (s *State) SetControls(pitchCents, pressure, timbre float32) {
	s.PitchCents, s.Pressure, s.Timbre = pitchCents, pressure, timbre
	s.updateRatio()
}

func (s *State) updateRatio() {
	s.ratio = 1
	if s.PitchCents != 0 {
		s.ratio = fastmath.Exp2(float64(s.PitchCents) / 1200)
	}
}

// NextRatio caches static bend and evaluates only a running vibrato per sample.
func (s *State) NextRatio() float64 {
	if s.VibratoDepthCents != 0 && s.increment != 0 {
		return fastmath.Exp2(s.NextCents() / 1200)
	}
	return s.ratio
}
