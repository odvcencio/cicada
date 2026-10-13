package paramdefs

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseBase turns the text of a numeric source value into a number in the
// base unit (Hz, ms, dB, a plain ratio) and names that unit. It is the one
// parser every compiler, validator and range check uses: a scaled literal such
// as 0.3s, 8kHz or 55% is converted by moving the decimal point in the text, so
// the result is the float64 nearest to the exact decimal, never a product that
// carries rounding error.
func ParseBase(source string) (float64, string, error) {
	unit := "unit"
	places := 0
	for _, suffix := range []struct {
		name, unit string
		places     int
	}{{"frames", "frames", 0}, {"lufs", "lufs", 0}, {"dbtp", "dbtp", 0}, {"khz", "hz", 3}, {"hz", "hz", 0}, {"ms", "ms", 0}, {"db", "db", 0}, {"lu", "lu", 0}, {"s", "ms", 3}, {"%", "unit", -2}} {
		if strings.HasSuffix(strings.ToLower(source), suffix.name) {
			unit, places = suffix.unit, suffix.places
			source = source[:len(source)-len(suffix.name)]
			break
		}
	}
	shifted, err := shiftDecimal(source, places)
	if err != nil {
		return 0, "", err
	}
	value, err := strconv.ParseFloat(shifted, 64)
	if err != nil {
		return 0, "", err
	}
	return value, unit, nil
}

// shiftDecimal moves a decimal point in the literal text, preserving exact
// base-unit conversion until the final float64 parse.
func shiftDecimal(source string, places int) (string, error) {
	sign := ""
	if strings.HasPrefix(source, "-") {
		sign, source = "-", source[1:]
	} else if strings.HasPrefix(source, "+") {
		sign, source = "+", source[1:]
	}
	point := strings.IndexByte(source, '.')
	if point < 0 {
		point = len(source)
	}
	digits := strings.ReplaceAll(source, ".", "")
	if digits == "" {
		return "", fmt.Errorf("invalid decimal value")
	}
	newPoint := point + places
	if newPoint <= 0 {
		digits = strings.Repeat("0", -newPoint) + digits
		digits = "0." + digits
	} else if newPoint >= len(digits) {
		digits += strings.Repeat("0", newPoint-len(digits))
	} else {
		digits = digits[:newPoint] + "." + digits[newPoint:]
	}
	return sign + digits, nil
}
