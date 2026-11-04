package notation

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/internal/paramdefs"
)

func IsVibratoSetting(name string) bool {
	return name == "vibrato" || name == "vibrato_rate" || name == "vibrato_delay"
}

// VibratoSetting parses a baseline setting in its required source unit.
func VibratoSetting(name, literal string) (float64, error) {
	value, unit, err := paramdefs.ParseBase(literal)
	want, max := "", float64(0)
	switch name {
	case "vibrato":
		want, max = "ct", 9600
	case "vibrato_rate":
		want, max = "hz", 100
	case "vibrato_delay":
		want, max = "ms", 10000
	default:
		return 0, fmt.Errorf("unknown vibrato setting %s", name)
	}
	if err != nil || unit != want || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > max {
		return 0, fmt.Errorf("%s must be 0 to %g%s", name, max, want)
	}
	return value, nil
}
