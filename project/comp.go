package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/fx"
)

// CompSpecFromValues lowers a built-in music-bus compressor declaration.
// An empty sidechain uses the music bus itself; a named track is external.
func CompSpecFromValues(values map[string]Value) (fx.CompParams, string, error) {
	params := fx.DefaultCompParams()
	var sidechain string
	for _, name := range sortedKeys(values) {
		value := values[name]
		switch name {
		case "detect":
			if value.Unit != "enum" || value.Number != nil {
				return params, sidechain, fmt.Errorf("compressor detect requires peak or rms")
			}
			switch value.Text {
			case "peak":
				params.Detect = fx.PeakDetector
			case "rms":
				params.Detect = fx.RMSDetector
			default:
				return params, sidechain, fmt.Errorf("compressor detect requires peak or rms")
			}
		case "threshold":
			if value.Unit != "db" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor threshold requires dB")
			}
			params.Threshold = *value.Number
		case "ratio":
			if value.Unit != "unit" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor ratio requires a unitless value")
			}
			params.Ratio = *value.Number
		case "knee":
			if value.Unit != "db" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor knee requires dB")
			}
			params.Knee = *value.Number
		case "attack":
			if value.Unit != "ms" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor attack requires milliseconds")
			}
			params.AttackMs = *value.Number
		case "release":
			if value.Unit != "ms" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor release requires milliseconds")
			}
			params.ReleaseMs = *value.Number
		case "makeup":
			if value.Unit == "enum" && value.Number == nil && value.Text == "auto" {
				params.MakeupAuto, params.MakeupDB = true, 0
			} else if value.Unit == "db" && value.Number != nil {
				params.MakeupAuto, params.MakeupDB = false, *value.Number
			} else {
				return params, sidechain, fmt.Errorf("compressor makeup requires auto or dB")
			}
		case "sidechain":
			if value.Unit != "enum" || value.Number != nil || !validID(value.Text) {
				return params, sidechain, fmt.Errorf("compressor sidechain requires a track ID")
			}
			sidechain = value.Text
		case "mix":
			if value.Unit != "unit" || value.Number == nil {
				return params, sidechain, fmt.Errorf("compressor mix requires a unitless value")
			}
			params.Mix = *value.Number
		default:
			return params, sidechain, fmt.Errorf("unknown compressor parameter %s", name)
		}
	}
	if err := params.Validate(); err != nil {
		return params, sidechain, err
	}
	return params, sidechain, nil
}
