package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/tape"
)

// isDelayReturnKind reports whether an effect kind can be the score's delay
// return, the destination of send A. A score has at most one such effect.
func isDelayReturnKind(kind string) bool { return kind == "delay" || kind == "tape" }

// TapeParamsFromValues reads the parameters of a `tape` effect. time takes a
// note division or milliseconds, like the delay.
func TapeParamsFromValues(values map[string]Value) (tape.Params, error) {
	params := tape.DefaultParams()
	for _, name := range sortedKeys(values) {
		value := values[name]
		unitless := func(target *float64) error {
			if value.Unit != "unit" || value.Number == nil {
				return fmt.Errorf("tape %s requires a unitless value", name)
			}
			*target = *value.Number
			return nil
		}
		var err error
		switch name {
		case "time":
			if value.Unit == "ms" && value.Number != nil {
				params.Division, params.TimeMs = fx.FreeDelay, *value.Number
			} else if value.Unit == "enum" && value.Number == nil {
				division, perr := fx.ParseDelayDivision(value.Text)
				if perr != nil || division == fx.FreeDelay {
					return params, fmt.Errorf("tape time requires a supported note division or 1ms..2s")
				}
				params.Division, params.TimeMs = division, 0
			} else {
				return params, fmt.Errorf("tape time requires a note division or milliseconds")
			}
		case "feedback":
			err = unitless(&params.Feedback)
		case "wow":
			err = unitless(&params.Wow)
		case "flutter":
			err = unitless(&params.Flutter)
		case "saturation":
			err = unitless(&params.Saturation)
		case "damp":
			if value.Unit != "hz" || value.Number == nil {
				return params, fmt.Errorf("tape damp requires Hz")
			}
			params.DampHz = *value.Number
		default:
			return params, fmt.Errorf("unknown tape parameter %s", name)
		}
		if err != nil {
			return params, err
		}
	}
	if err := params.Validate(); err != nil {
		return params, err
	}
	return params, nil
}
