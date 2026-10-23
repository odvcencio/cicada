package control

import "math"

const MaxExpressionPoints = 65536

// NoteExpression is an absolute input tick and a complete controller snapshot.
// Keeping every received snapshot preserves the raw performance before editing.
type NoteExpression struct {
	Tick              int64    `json:"tick"`
	PitchCents        float64  `json:"pitchCents"`
	Pressure          float64  `json:"pressure"`
	Timbre            float64  `json:"timbre"`
	VibratoDepthCents *float64 `json:"vibratoDepthCents,omitempty"`
}

func (e NoteExpression) Valid() bool {
	for _, value := range []struct{ value, low, high float64 }{{e.PitchCents, -9600, 9600}, {e.Pressure, 0, 1}, {e.Timbre, 0, 1}} {
		if math.IsNaN(value.value) || math.IsInf(value.value, 0) || value.value < value.low || value.value > value.high {
			return false
		}
	}
	return e.VibratoDepthCents == nil || !math.IsNaN(*e.VibratoDepthCents) && !math.IsInf(*e.VibratoDepthCents, 0) && *e.VibratoDepthCents >= 0 && *e.VibratoDepthCents <= 9600
}

// AddExpression never replaces a raw point, including two at the same tick.
func (n *Note) AddExpression(value NoteExpression) {
	value.Tick = max(n.Tick, value.Tick)
	if len(n.Expressions) > 0 {
		value.Tick = max(value.Tick, n.Expressions[len(n.Expressions)-1].Tick)
	}
	n.Expressions = append(n.Expressions, value)
	n.EndTick = max(n.EndTick, value.Tick)
}
