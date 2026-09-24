package project

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/notation"
)

// CompileAcidParams validates authored built-in voice parameters before a
// score can render. Numeric time values are converted from milliseconds to
// the acid voice's seconds-based runtime representation.
func CompileAcidParams(track notation.Track) (acid.Params, error) {
	params := acid.DefaultParams()
	for _, source := range track.Params {
		if source.Name == "level" || source.Name == "pan" || source.Name == "insert" || source.Name == "send_a" || source.Name == "send_b" || source.Name == "send_pre" {
			continue
		}
		if source.Name == "octave" {
			value, err := strconv.Atoi(source.Value)
			if err != nil || value < 0 || value > 6 {
				return params, fmt.Errorf("octave must be 0 to 6")
			}
			continue
		}
		if source.Name == "filter" || source.Name == "savage" {
			if err := applyAcidParam(&params, source.Name, 0, "enum", source.Value); err != nil {
				return params, err
			}
			continue
		}
		value, unit, err := parseBaseValue(source.Value)
		if err != nil {
			return params, fmt.Errorf("%s: %w", source.Name, err)
		}
		if err := applyAcidParam(&params, source.Name, value, unit, ""); err != nil {
			return params, err
		}
	}
	return params, params.Validate()
}

func acidParamsFromValues(values map[string]Value) (acid.Params, error) {
	params := acid.DefaultParams()
	for _, name := range sortedKeys(values) {
		value := values[name]
		if name == "octave" {
			if value.Number == nil || value.Unit != "unit" || *value.Number < 0 || *value.Number > 6 || *value.Number != float64(int(*value.Number)) {
				return params, fmt.Errorf("octave must be an integer from 0 to 6")
			}
			continue
		}
		number := 0.0
		if value.Number != nil {
			number = *value.Number
		}
		if err := applyAcidParam(&params, name, number, value.Unit, value.Text); err != nil {
			return params, err
		}
	}
	return params, params.Validate()
}

func applyAcidParam(params *acid.Params, name string, value float64, unit, text string) error {
	wantUnit := "unit"
	switch name {
	case "cutoff":
		wantUnit = "hz"
	case "decay", "release", "slide":
		wantUnit = "ms"
	case "filter", "savage":
		wantUnit = "enum"
	case "tune", "fine", "wave", "pw", "detune", "sub", "reso", "envmod", "accent", "drive", "gate":
	default:
		return fmt.Errorf("unknown acid parameter %s", name)
	}
	if unit != wantUnit {
		return fmt.Errorf("%s requires %s, got %s", name, wantUnit, unit)
	}
	switch name {
	case "tune":
		params.Tune = value
	case "fine":
		params.Fine = value
	case "wave":
		params.Wave = value
	case "pw":
		params.PulseWidth = value
	case "detune":
		params.Detune = value
	case "sub":
		params.Sub = value
	case "cutoff":
		params.Cutoff = value
	case "reso":
		params.Resonance = value
	case "envmod":
		params.EnvMod = value
	case "decay":
		params.Decay = value / 1000
	case "accent":
		params.Accent = value
	case "drive":
		params.Drive = value
	case "release":
		params.Release = value / 1000
	case "slide":
		params.Slide = value / 1000
	case "gate":
		params.Gate = value
	case "filter":
		switch text {
		case "diode":
			params.Filter = acid.Diode
		case "ladder":
			params.Filter = acid.Ladder
		default:
			return fmt.Errorf("unknown acid filter %q", text)
		}
	case "savage":
		switch text {
		case "on", "true":
			params.Savage = true
		case "off", "false":
			params.Savage = false
		default:
			return fmt.Errorf("unknown savage value %q", text)
		}
	}
	return nil
}
