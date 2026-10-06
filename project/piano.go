package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/notation"
)

func isModeledPiano(p *Project, kind string) bool {
	if kind != "piano" {
		return false
	}
	for _, instrument := range p.Instruments {
		if instrument.ID == kind {
			return false
		}
	}
	for _, kit := range p.Kits {
		if kit.ID == kind {
			return false
		}
	}
	for _, sampler := range p.Samplers {
		if sampler.Name == kind {
			return false
		}
	}
	return true
}

// PianoSustainFromValues validates the modeled piano's authored controls.
func PianoSustainFromValues(values map[string]Value) (float32, error) {
	var sustain float32
	for name, value := range values {
		switch name {
		case "sustain":
			if value.Unit != "unit" || value.Number == nil || math.IsNaN(*value.Number) || math.IsInf(*value.Number, 0) || *value.Number < 0 || *value.Number > 1 {
				return 0, fmt.Errorf("piano sustain must be a unitless value from 0 to 1")
			}
			sustain = float32(*value.Number)
		case "octave":
			if err := validateOctaveValue(value); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("unknown piano parameter %s", name)
		}
	}
	return sustain, nil
}

// CompilePianoSustain compiles the same source control used by live playback.
func CompilePianoSustain(track notation.Track) (float32, error) {
	values := make(map[string]Value)
	for _, parameter := range track.Params {
		if IsMixerSourceParam(parameter.Name) {
			continue
		}
		value, err := projectValue(parameter.Value)
		if err != nil {
			return 0, err
		}
		values[parameter.Name] = value
	}
	return PianoSustainFromValues(values)
}
