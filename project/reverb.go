package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/fx"
)

func ReverbParamsFromValues(values map[string]Value) (fx.ReverbParams, error) {
	params := fx.DefaultReverbParams()
	for _, name := range sortedKeys(values) {
		value := values[name]
		if value.Number == nil {
			return params, fmt.Errorf("reverb %s requires a numeric value", name)
		}
		switch name {
		case "size":
			if value.Unit != "unit" {
				return params, fmt.Errorf("reverb size requires a unitless value")
			}
			params.Size = *value.Number
		case "decay":
			if value.Unit != "ms" {
				return params, fmt.Errorf("reverb decay requires a time")
			}
			params.DecaySec = *value.Number / 1000
		case "damp":
			if value.Unit != "hz" {
				return params, fmt.Errorf("reverb damp requires Hz")
			}
			params.DampHz = *value.Number
		case "highpass":
			if value.Unit != "hz" {
				return params, fmt.Errorf("reverb highpass requires Hz")
			}
			params.HighpassHz = *value.Number
		case "predelay":
			if value.Unit != "ms" {
				return params, fmt.Errorf("reverb predelay requires a time")
			}
			params.PredelayMs = *value.Number
		case "mix":
			if value.Unit != "unit" {
				return params, fmt.Errorf("reverb mix requires a unitless value")
			}
			params.Mix = *value.Number
		default:
			return params, fmt.Errorf("unknown reverb parameter %s", name)
		}
	}
	if err := params.Validate(); err != nil {
		return params, err
	}
	return params, nil
}
