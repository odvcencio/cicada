package notation

import (
	"strconv"

	"m31labs.dev/cicada/instrument/keyboardpresets"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

// Keyboard presets use the native patch controls without changing the kernel ABI.
func keysPresetDescriptors(name string) []paramdefs.Descriptor {
	spec, err := keyboardpresets.DefaultSpec(name)
	if err != nil {
		return nil
	}
	var descriptors []paramdefs.Descriptor
	for _, parameter := range keyboard.Parameters(name) {
		unit, scale := parameter.Unit, 1.0
		if unit == "cents" {
			unit = "unit" // Score numbers use cents as the implicit base unit.
		}
		if unit == "s" {
			unit, scale = "ms", 1000
		}
		curve := "linear"
		if parameter.Integer {
			curve = "integer"
		}
		canonical := func(value float32) float64 {
			number, _ := strconv.ParseFloat(strconv.FormatFloat(float64(value), 'g', -1, 32), 64)
			return number * scale
		}
		descriptors = append(descriptors, paramdefs.Descriptor{
			ID: "keys." + name + "." + parameter.Name, Path: parameter.Name,
			Scope: "track", Voices: []string{"keys"}, Source: parameter.Name,
			Unit: unit, Min: canonical(parameter.Min), Max: canonical(parameter.Max),
			Default: canonical(spec.Controls[parameter.Index]), Curve: curve,
			Values: keysPresetAliases(parameter.Name),
		})
	}
	return descriptors
}

func keysPresetAliases(name string) []string {
	switch name {
	case "pickup":
		return []string{"bridge", "neck", "both", "difference"}
	case "percussion":
		return []string{"off", "second", "third"}
	case "scanner":
		return []string{"off", "v1", "v2", "v3", "c1", "c2", "c3"}
	case "filter":
		return []string{"ladder", "state_variable"}
	case "percussion_fast", "rotary_fast":
		return []string{"slow", "fast"}
	case "percussion_soft":
		return []string{"normal", "soft"}
	case "sustain":
		return []string{"off", "on"}
	}
	return nil
}
