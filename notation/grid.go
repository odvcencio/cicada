package notation

import (
	"fmt"
	"strconv"
	"strings"
)

// GridTicks resolves a note division without rounding at 960 PPQ.
func GridTicks(text string) (uint16, error) {
	numerator, denominator := int64(3840), int64(1)
	if strings.HasSuffix(strings.ToLower(text), "t") {
		numerator *= 2
		denominator *= 3
		text = text[:len(text)-1]
	} else if strings.HasSuffix(text, ".") {
		numerator *= 3
		denominator *= 2
		text = text[:len(text)-1]
	}
	parts := strings.Split(text, "/")
	if len(parts) == 2 {
		n, e1 := strconv.ParseInt(parts[0], 10, 16)
		d, e2 := strconv.ParseInt(parts[1], 10, 16)
		if e1 == nil && e2 == nil && n > 0 && d > 0 {
			numerator *= n
			denominator *= d
			if numerator%denominator == 0 && numerator/denominator >= 30 && numerator/denominator <= 3840 {
				return uint16(numerator / denominator), nil
			}
		}
	}
	return 0, fmt.Errorf("step %q must resolve exactly to 30..3840 ticks at 960 PPQ", text)
}
