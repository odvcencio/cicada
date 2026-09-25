// Package phrase generates deterministic musical phrases outside the audio callback.
package phrase

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/seq"
)

type Scale uint8

const (
	Minor Scale = iota
	Phrygian
	Dorian
	HarmonicMinor
	MinorPent
	Major
	Mixolydian
	Blues
)

type Structure uint8

const (
	A Structure = iota + 1
	AABA
	ABAB
	ABAC
	AAAB
)

type Params struct {
	Seed            uint64
	Key             uint8
	Scale           Scale
	RootOctave      uint8
	Steps           uint8
	Density         float32
	AccentDensity   float32
	SlideDensity    float32
	OctaveJump      float32
	SwingPercent100 uint16
	GatePercent     uint8
	RestDownbeat    bool
	Structure       Structure
}

type Draw struct {
	Pass   string `json:"pass"`
	Step   int    `json:"step"`
	Raw    uint32 `json:"raw"`
	Choice int    `json:"choice"`
}

type OpKind uint8

const (
	NudgeDegree OpKind = iota + 1
	ToggleAccent
	ToggleSlide
	OctaveFlip
	RotateRhythm
	SwapSteps
	FillRest
	Thin
	Ratchet
)

type Op struct {
	Kind OpKind
	Arg  int
}

type Result struct {
	Bars     []seq.Pattern
	Notation string
	Trace    []Draw
}

type normalizedParams struct {
	Params
	density, accentDensity, slideDensity, octaveJump uint8
}

func DefaultParams() Params {
	return Params{
		RootOctave: 2, Steps: 16, Density: 0.6,
		AccentDensity: 0.5, SlideDensity: 0.4, OctaveJump: 0.3,
		SwingPercent100: 5400, GatePercent: 55, Structure: AABA,
	}
}

func normalize(p Params) (normalizedParams, error) {
	if p.Key > 11 || p.Scale > Blues {
		return normalizedParams{}, fmt.Errorf("invalid key or scale")
	}
	if p.RootOctave == 0 {
		p.RootOctave = 2
	}
	if p.RootOctave > 4 {
		return normalizedParams{}, fmt.Errorf("root octave must be 1 to 4")
	}
	if p.Steps == 0 {
		p.Steps = 16
	}
	if p.Steps != 8 && p.Steps != 16 && p.Steps != 32 && p.Steps != 64 {
		return normalizedParams{}, fmt.Errorf("steps must be 8, 16, 32, or 64")
	}
	if p.SwingPercent100 == 0 {
		p.SwingPercent100 = 5400
	}
	if p.SwingPercent100 < 5000 || p.SwingPercent100 > 7500 {
		return normalizedParams{}, fmt.Errorf("swing must be 50 to 75 percent")
	}
	if p.GatePercent == 0 {
		p.GatePercent = 55
	}
	if p.GatePercent < 10 || p.GatePercent > 100 {
		return normalizedParams{}, fmt.Errorf("gate must be 10 to 100 percent")
	}
	if p.Structure == 0 {
		p.Structure = AABA
	}
	if p.Structure < A || p.Structure > AAAB {
		return normalizedParams{}, fmt.Errorf("invalid structure")
	}
	density, err := quantizeDensity(p.Density)
	if err != nil {
		return normalizedParams{}, fmt.Errorf("density: %w", err)
	}
	accentDensity, err := quantizeDensity(p.AccentDensity)
	if err != nil {
		return normalizedParams{}, fmt.Errorf("accent density: %w", err)
	}
	slideDensity, err := quantizeDensity(p.SlideDensity)
	if err != nil {
		return normalizedParams{}, fmt.Errorf("slide density: %w", err)
	}
	octaveJump, err := quantizeDensity(p.OctaveJump)
	if err != nil {
		return normalizedParams{}, fmt.Errorf("octave jump: %w", err)
	}
	return normalizedParams{p, density, accentDensity, slideDensity, octaveJump}, nil
}

func quantizeDensity(value float32) (uint8, error) {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value < 0 || value > 1 {
		return 0, fmt.Errorf("must be finite and between 0 and 1")
	}
	return uint8(math.Floor(float64(value)*64 + 0.5)), nil
}
