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

// PositionTick converts one-based bar.beat.sixteenth positions at 960 PPQ.
func PositionTick(at string) (int64, error) {
	parts := strings.Split(strings.TrimPrefix(at, "@"), ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("position must be @bar.beat.step")
	}
	var n [3]int64
	for i, part := range parts {
		value, err := strconv.ParseInt(part, 10, 32)
		if err != nil || value < 1 {
			return 0, fmt.Errorf("position needs bar >= 1, beat 1..4 and step 1..4")
		}
		n[i] = value
	}
	// Automation reads positions before a score's meter is known: 4/4.
	tick, ok := seq.MeterMap{}.PositionTick(n[0], n[1], n[2])
	if !ok {
		return 0, fmt.Errorf("position needs bar >= 1, beat 1..4 and step 1..4")
	}
	return tick, nil
}

func TickPosition(tick int64) string {
	bar, beat, step, _ := seq.MeterMap{}.Position(tick)
	return fmt.Sprintf("@%d.%d.%d", bar, beat, step)
}
