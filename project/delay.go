package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/fx"
)

func DelayParamsFromValues(values map[string]Value) (fx.DelayParams, error) {
	params := fx.DefaultDelayParams()
	for _, name := range sortedKeys(values) {
		value := values[name]
		switch name {
		case "time":
			if value.Unit == "ms" && value.Number != nil {
				params.Division, params.TimeMs = fx.FreeDelay, *value.Number
			} else if value.Unit == "enum" && value.Number == nil {
				division, err := fx.ParseDelayDivision(value.Text)
				if err != nil || division == fx.FreeDelay {
					return params, fmt.Errorf("delay time requires a supported note division or 1ms..2s")
				}
				params.Division, params.TimeMs = division, 0
			} else {
				return params, fmt.Errorf("delay time requires a note division or milliseconds")
			}
		case "feedback":
			if value.Unit != "unit" || value.Number == nil {
				return params, fmt.Errorf("delay feedback requires a unitless value")
			}
			params.Feedback = *value.Number
		case "damp":
			if value.Unit != "hz" || value.Number == nil {
				return params, fmt.Errorf("delay damp requires Hz")
			}
			params.DampHz = *value.Number
		case "pingpong":
			if value.Unit != "enum" || value.Number != nil {
				return params, fmt.Errorf("delay pingpong requires on, off, true, or false")
			}
			switch value.Text {
			case "on", "true":
				params.PingPong = true
			case "off", "false":
				params.PingPong = false
			default:
				return params, fmt.Errorf("delay pingpong requires on, off, true, or false")
			}
		case "width":
			if value.Unit != "unit" || value.Number == nil {
				return params, fmt.Errorf("delay width requires a unitless value")
			}
			params.Width = *value.Number
		case "mix":
			if value.Unit != "unit" || value.Number == nil {
				return params, fmt.Errorf("delay mix requires a unitless value")
			}
			params.Mix = *value.Number
		case "highpass":
			hz, err := echoHighpassValue("delay", value)
			if err != nil {
				return params, err
			}
			params.HighpassHz = hz
		default:
			return params, fmt.Errorf("unknown delay parameter %s", name)
		}
	}
	if err := params.Validate(); err != nil {
		return params, err
	}
	return params, nil
}

// echoHighpassValue reads the highpass of a delay or tape: a frequency in Hz,
// or off, which is a cutoff of 0.
func echoHighpassValue(kind string, value Value) (float64, error) {
	if value.Unit == "enum" && value.Number == nil && value.Text == "off" {
		return 0, nil
	}
	if value.Unit != "hz" || value.Number == nil {
		return 0, fmt.Errorf("%s highpass requires a frequency from %dHz to %dHz, or off", kind, fx.MinEchoHighpassHz, fx.MaxEchoHighpassHz)
	}
	if !fx.ValidEchoHighpass(*value.Number) || *value.Number == 0 {
		return 0, fmt.Errorf("%s highpass %gHz is outside %dHz to %dHz; write off to turn it off", kind, *value.Number, fx.MinEchoHighpassHz, fx.MaxEchoHighpassHz)
	}
	return *value.Number, nil
}
