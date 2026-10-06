package notation

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ExpressionValue parses a non-hold row cell using the source row's units.
func ExpressionValue(name, text string) (float32, error) {
	minimum, maximum := float64(0), float64(1)
	unit := ""
	switch name {
	case "bend":
		minimum, maximum, unit = -9600, 9600, "ct"
	case "vibrato":
		maximum, unit = 9600, "ct"
	case "pressure", "timbre":
	default:
		return 0, fmt.Errorf("unknown expression row %s", name)
	}
	if unit != "" && !strings.HasSuffix(text, unit) {
		return 0, fmt.Errorf("%s values require cents (ct)", name)
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(text, unit), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be %g to %g%s", name, minimum, maximum, unit)
	}
	return float32(value), nil
}
