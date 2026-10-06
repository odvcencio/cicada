package modal

import "math"

// MaxModes bounds fitted models as well as built-in profiles.
const MaxModes = maxModes

type Mode struct {
	Ratio, Weight, T60 float64
}

// Model contains only poles and excitation colour; no recorded PCM.
type Model struct {
	Count    int
	Modes    [MaxModes]Mode
	NoiseMix float64
}

// FittedVoice reuses the modal engine's contact, resonator, glide and stealing
// paths with measured poles. Construction happens outside audio rendering.
type FittedVoice struct {
	*Voice
	model Model
}

func NewFittedVoice(model Model, rate int) (*FittedVoice, error) {
	if model.Count < 1 || model.Count > MaxModes || math.IsNaN(model.NoiseMix) || model.NoiseMix < 0 || model.NoiseMix > 1 {
		return nil, Error("invalid fitted modal model")
	}
	weight := 0.0
	for _, m := range model.Modes[:model.Count] {
		if math.IsNaN(m.Ratio+m.Weight+m.T60) || math.IsInf(m.Ratio+m.Weight+m.T60, 0) || m.Ratio < 1 || m.Ratio > 1024 || m.Weight <= 0 || m.Weight > 1 || m.T60 < .01 || m.T60 > 3 {
			return nil, Error("invalid fitted modal pole")
		}
		weight += m.Weight
	}
	if weight <= 0 {
		return nil, Error("empty fitted modal weights")
	}
	v, err := NewVoice(Wood, rate)
	if err != nil {
		return nil, err
	}
	return &FittedVoice{Voice: v, model: model}, nil
}

// ResetVariation selects a repeatable contact position after clearing tails.
func (v *FittedVoice) ResetVariation(variation uint32) {
	v.Reset()
	v.sequence = variation & 3
}

func (v *FittedVoice) NoteOn(note, velocity uint8, slide bool) {
	glide := slide && v.latest >= 0 && v.strikes[v.latest].active
	variation := v.sequence & 3
	v.Voice.NoteOn(note, velocity, slide)
	if velocity == 0 || glide {
		return
	}
	s := &v.strikes[v.latest]
	s.modes, s.count = [MaxModes]resonator{}, 0
	s.noiseMix = v.model.NoiseMix
	frequency := v.pitch(min(note, 127))
	weightSum := 0.0
	for _, m := range v.model.Modes[:v.model.Count] {
		if frequency*m.Ratio < .45*v.rate {
			weightSum += m.Weight
		}
	}
	vel := float64(min(velocity, 127)) / 127
	for i, m := range v.model.Modes[:v.model.Count] {
		freq := frequency * m.Ratio
		if freq >= .45*v.rate {
			continue
		}
		mode := &s.modes[s.count]
		mode.freq, mode.active = freq, true
		mode.radius = math.Exp(-math.Log(1000) / (m.T60 * v.rate))
		mode.s, mode.c = math.Sincos(2 * math.Pi * freq / v.rate)
		mode.c, mode.s = mode.c*mode.radius, mode.s*mode.radius
		brightness := 1.0
		if i > 0 {
			brightness = .25 + .75*vel*vel
		}
		position := 1 + .055*math.Sin(float64((i+1)*int(variation+1))*1.7)
		mode.gain = .9 * vel * math.Sqrt(vel) * m.Weight / weightSum * brightness * position
		s.count++
	}
	s.active = s.count > 0
}
