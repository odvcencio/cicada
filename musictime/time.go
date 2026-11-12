// Package musictime converts exact musical positions and durations to ticks.
// It is shared by the score compiler and browser controls.
package musictime

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
)

// MusicalTicks accepts exact positions, bars, ticks, or note divisions. A
// position and a bar count come from the meter map; a note division is a
// fraction of a whole note. Values outside the fixed tick resolution fail
// instead of being rounded.
func MusicalTicks(meterMap seq.MeterMap, value string, position bool) (int64, error) {
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
		// The bar must end by 2^53-1 ticks; a bar is at least one step long.
		if n[0] > (1<<53-1)/seq.TicksPerStep || meterMap.BarStart(n[0]) > 1<<53-1 {
			return 0, fmt.Errorf("position outside 4/4 grid: %q", value)
		}
		tick, ok := meterMap.PositionTick(n[0], n[1], n[2])
		if !ok {
			return 0, fmt.Errorf("position outside 4/4 grid: %q", value)
		}
		return tick, nil
	}
	multiplier := int64(1)
	number := value
	switch {
	case strings.HasSuffix(value, "ticks"):
		number = strings.TrimSuffix(value, "ticks")
	case strings.HasSuffix(value, "bars"), strings.HasSuffix(value, "bar"):
		if number = strings.TrimSuffix(value, "bars"); number == value {
			number = strings.TrimSuffix(value, "bar")
		}
		multiplier = meterMap.BarTicks(0)
	case strings.Contains(value, "/") && !position:
		multiplier = seq.TicksPerWhole
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
