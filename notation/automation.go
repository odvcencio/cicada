package notation

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
)

type Automation struct {
	Path     string
	Points   []AutomationPoint
	Recalls  []AutomationRecall
	Position Position
}

type AutomationRecall struct {
	At       string
	Position Position
}

type AutomationPoint struct {
	At, Value, Shape, Curve string
	Position                Position
}

// PositionTick converts a one-based @bar.beat.step on the meter map to a tick.
func PositionTick(meterMap seq.MeterMap, at string) (int64, error) {
	parts := strings.Split(strings.TrimPrefix(at, "@"), ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("position must be @bar.beat.step")
	}
	var n [3]int64
	for i, part := range parts {
		value, err := strconv.ParseInt(part, 10, 32)
		if err != nil || value < 1 {
			return 0, fmt.Errorf("position %s needs a bar, beat and step counted from 1", at)
		}
		n[i] = value
	}
	tick, ok := meterMap.PositionTick(n[0], n[1], n[2])
	if !ok {
		// The arrangement's message names the bar and its meter's ranges.
		_, err := MusicalTicks(meterMap, "@"+strings.TrimPrefix(at, "@"), true)
		return 0, err
	}
	return tick, nil
}

// TickPosition spells a tick as @bar.beat.step on the meter map.
func TickPosition(meterMap seq.MeterMap, tick int64) string {
	bar, beat, step, _ := meterMap.Position(tick)
	return fmt.Sprintf("@%d.%d.%d", bar, beat, step)
}
