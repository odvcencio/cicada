package edit

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
)

// FormatPosition spells a tick as "@bar.beat.step" when it lies on the step
// grid, and as "<n>ticks" otherwise, so an off-grid value is still exact.
func FormatPosition(tick int64) string {
	if tick < 0 || tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%dticks", tick)
	}
	bar := tick/seq.TicksPerBar + 1
	beat := tick%seq.TicksPerBar/seq.PPQ + 1
	step := tick%seq.PPQ/seq.TicksPerStep + 1
	return fmt.Sprintf("@%d.%d.%d", bar, beat, step)
}

// FormatLength spells whole bars as "1bar" or "<n>bars" and any other length
// as "<n>ticks".
func FormatLength(ticks int64) string {
	switch {
	case ticks > 0 && ticks%seq.TicksPerBar == 0 && ticks == seq.TicksPerBar:
		return "1bar"
	case ticks > 0 && ticks%seq.TicksPerBar == 0:
		return fmt.Sprintf("%dbars", ticks/seq.TicksPerBar)
	}
	return fmt.Sprintf("%dticks", ticks)
}
