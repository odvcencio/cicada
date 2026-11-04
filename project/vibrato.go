package project

import (
	"fmt"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/notation"
)

// trackVibratoSettings resolves per-track overrides independently, preserving
// legacy graph parameters whose names happen to match these settings.
func trackVibratoSettings(base *expression.VibratoConfig, values map[string]Value, program *instrument.Program) (*expression.VibratoConfig, error) {
	result := expression.VibratoConfig{RateHz: 5}
	set := base != nil
	if base != nil {
		result = *base
	}
	for _, name := range []string{"vibrato", "vibrato_rate", "vibrato_delay"} {
		value, ok := values[name]
		if !ok || program != nil && program.HasParameter(name) {
			continue
		}
		literal, err := valueSource(value)
		if err != nil {
			return nil, err
		}
		n, err := notation.VibratoSetting(name, literal)
		if err != nil {
			return nil, err
		}
		set = true
		switch name {
		case "vibrato":
			result.DepthCents = float32(n)
		case "vibrato_rate":
			result.RateHz = float32(n)
		case "vibrato_delay":
			result.DelayMS = float32(n)
		}
	}
	if !set {
		return nil, nil
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("invalid vibrato: %w", err)
	}
	return &result, nil
}
