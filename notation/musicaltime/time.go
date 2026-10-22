// Package musicaltime parses exact musical positions without linking a score parser.
package musicaltime

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
)

// Parse accepts exact 4/4 positions, bars, ticks, or note divisions.
// Values outside the fixed tick resolution fail instead of being rounded.
func Parse(value string, position bool) (int64, error) {
	if strings.HasPrefix(value, "@") && position {
		parts := strings.Split(value[1:], ".")
		if len(parts) != 3 {
			return 0, fmt.Errorf("expected @bar.beat.step")
		}
		var n [3]int64
		for i, part := range parts {
			v, err := strconv.ParseInt(part, 10, 64)
			if err != nil || v < 1 {
				return 0, fmt.Errorf("invalid musical position %q", value)
			}
			n[i] = v
		}
		if n[0] > (1<<53-1)/seq.TicksPerBar || n[1] > 4 || n[2] > 4 {
			return 0, fmt.Errorf("position outside 4/4 grid: %q", value)
		}
		return (n[0]-1)*seq.TicksPerBar + (n[1]-1)*seq.PPQ + (n[2]-1)*seq.TicksPerStep, nil
	}
	multiplier := int64(1)
	number := value
	switch {
	case strings.HasSuffix(value, "ticks"):
		number = strings.TrimSuffix(value, "ticks")
	case strings.HasSuffix(value, "bars"):
		number = strings.TrimSuffix(value, "bars")
		multiplier = seq.TicksPerBar
	case strings.HasSuffix(value, "bar"):
		number = strings.TrimSuffix(value, "bar")
		multiplier = seq.TicksPerBar
	case strings.Contains(value, "/") && !position:
		multiplier = seq.TicksPerBar
	default:
		return 0, fmt.Errorf("expected exact ticks, bars, or note division; actual %q", value)
	}
	r, ok := new(big.Rat).SetString(number)
	if !ok {
		return 0, fmt.Errorf("invalid musical time %q", value)
	}
	r.Mul(r, big.NewRat(multiplier, 1))
	if !r.IsInt() || !r.Num().IsInt64() || r.Sign() < 0 || r.Num().Int64() > 1<<53-1 || !position && r.Sign() == 0 {
		return 0, fmt.Errorf("expected exact nonnegative musical ticks; actual %q", value)
	}
	return r.Num().Int64(), nil
}
