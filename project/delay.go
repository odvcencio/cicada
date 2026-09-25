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
				return params, fmt.Errorf("delay pingpong requires true or false")
			}
			switch value.Text {
			case "true":
				params.PingPong = true
			case "false":
				params.PingPong = false
			default:
				return params, fmt.Errorf("delay pingpong requires true or false")
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
		default:
			return params, fmt.Errorf("unknown delay parameter %s", name)
		}
	}
	if err := params.Validate(); err != nil {
		return params, err
	}
	return params, nil
}
