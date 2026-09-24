package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/fx"
)

// DriveParamsFromValues validates a built-in drive declaration in the typed
// project. Omitted fields use the versioned v1 defaults.
func DriveParamsFromValues(values map[string]Value) (fx.DriveParams, error) {
	params := fx.DefaultDriveParams()
	for _, name := range sortedKeys(values) {
		value := values[name]
		switch name {
		case "shape":
			if value.Unit != "enum" || value.Number != nil {
				return params, fmt.Errorf("drive shape requires soft, hard, fold, or diode")
			}
			switch value.Text {
			case "soft":
				params.Shape = fx.Soft
			case "hard":
				params.Shape = fx.Hard
			case "fold":
				params.Shape = fx.Fold
			case "diode":
				params.Shape = fx.Diode
			default:
				return params, fmt.Errorf("unknown drive shape %q", value.Text)
			}
		case "gain":
			if value.Unit != "db" || value.Number == nil {
				return params, fmt.Errorf("drive gain requires dB")
			}
			params.GainDB = *value.Number
		case "tone":
			if value.Unit != "hz" || value.Number == nil {
				return params, fmt.Errorf("drive tone requires Hz")
			}
			params.ToneHz = *value.Number
		case "mix":
			if value.Unit != "unit" || value.Number == nil {
				return params, fmt.Errorf("drive mix requires a unitless value")
			}
			params.Mix = *value.Number
		default:
			return params, fmt.Errorf("unknown drive parameter %s", name)
		}
	}
	if err := params.Validate(); err != nil {
		return params, err
	}
	return params, nil
}
