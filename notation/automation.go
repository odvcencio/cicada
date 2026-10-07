package notation

import (
	"fmt"
	"strconv"
	"strings"
)

type Automation struct {
	Path     string
	Points   []AutomationPoint
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
		if err != nil || value < 1 || i > 0 && value > 4 {
			return 0, fmt.Errorf("position needs bar >= 1, beat 1..4 and step 1..4")
		}
		n[i] = value
	}
	return (n[0]-1)*3840 + (n[1]-1)*960 + (n[2]-1)*240, nil
}

func TickPosition(tick int64) string {
	return fmt.Sprintf("@%d.%d.%d", tick/3840+1, tick%3840/960+1, tick%960/240+1)
}
