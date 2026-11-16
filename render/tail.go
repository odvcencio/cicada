package render

import (
	"fmt"
	"math"
	"strconv"

	"m31labs.dev/cicada/project"
)

// MaxTailSeconds is the longest tail a render, a stem render or a WAV check
// takes, in seconds. A reverb decay reaches 12 s, and a long echo with high
// feedback rings longer, so the tail holds several decay times. The renderer
// streams its output one block at a time, so a longer tail costs output size
// and render time, not memory.
const MaxTailSeconds = project.MaxRenderTailSeconds

// TailRangeError is a tail outside 0 to MaxTailSeconds, or one that is not a
// finite number.
type TailRangeError struct {
	Seconds float64
}

func (e *TailRangeError) Error() string {
	return fmt.Sprintf("tail %s is outside the 0 to %ds range", FormatTailSeconds(e.Seconds), MaxTailSeconds)
}

// FormatTailSeconds writes a tail in seconds the way the --tail flag takes it,
// such as 10.5s.
func FormatTailSeconds(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return strconv.FormatFloat(seconds, 'g', -1, 64)
	}
	return strconv.FormatFloat(seconds, 'f', -1, 64) + "s"
}

// checkTail returns a *TailRangeError for a tail the renderer does not take.
func checkTail(seconds float64) error {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > MaxTailSeconds {
		return &TailRangeError{Seconds: seconds}
	}
	return nil
}
