package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/notation"
)

// ModalSustainFromValues validates a modeled percussion track's authored
// controls and returns its damper pedal: on is 1, off is 0, and a number from 0
// to 1 is kept, as the vibraphone lifts its dampers from 0.5.
func ModalSustainFromValues(values map[string]Value) (float32, error) {
	var sustain float32
	for name, value := range values {
		switch name {
		case "sustain":
			switch {
			case value.Unit == "enum" && value.Text == "on":
				sustain = 1
			case value.Unit == "enum" && value.Text == "off":
				sustain = 0
			case value.Unit != "unit" || value.Number == nil || math.IsNaN(*value.Number) || math.IsInf(*value.Number, 0) || *value.Number < 0 || *value.Number > 1:
				return 0, fmt.Errorf("modal sustain must be on, off or a unitless value from 0 to 1")
			default:
				sustain = float32(*value.Number)
			}
		case "octave":
			if err := validateOctaveValue(value); err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("modeled percussion accepts only octave, sustain and mixer settings, not %s", name)
		}
	}
	return sustain, nil
}

// CompileModalSustain compiles the same source control used by live playback.
func CompileModalSustain(track notation.Track) (float32, error) {
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
	return ModalSustainFromValues(values)
}
