package edit

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
)

// FormatPosition spells a tick as "@bar.beat.step" on the meter map when it
// lies on the step grid, and as "<n>ticks" otherwise, so an off-grid value is
// still exact.
func FormatPosition(meterMap seq.MeterMap, tick int64) string {
	if tick < 0 || tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%dticks", tick)
	}
	bar, beat, step, _ := meterMap.Position(tick)
	return fmt.Sprintf("@%d.%d.%d", bar, beat, step)
}

// FormatLength spells a length that covers whole bars of the meter map, from
// the bar holding from, as "1bar" or "<n>bars", and any other length as
// "<n>ticks".
func FormatLength(meterMap seq.MeterMap, from, ticks int64) string {
	bars, whole := meterMap.WholeBars(from, ticks)
	switch {
	case ticks > 0 && whole && bars == 1:
		return "1bar"
	case ticks > 0 && whole:
		return fmt.Sprintf("%dbars", bars)
	}
	return fmt.Sprintf("%dticks", ticks)
}
