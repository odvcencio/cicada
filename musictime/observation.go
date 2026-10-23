package musictime

import "math"

// ObservedTempoStep is the next authored tempo boundary, measured from one
// rendered transport observation. It is supplemental clock metadata, not a
// replacement for the audio clock or a prediction of future live edits.
type ObservedTempoStep struct {
	AfterNanos int64 `json:"afterNanos"`
	Tick       int64 `json:"tick"`
	BPMMilli   int64 `json:"bpmMilli"`
}

// Valid checks the short horizon and continuity with the observed position.
// Integer sample/tick conversion can leave the observation less than one tick
// behind its fractional position at a boundary.
func (s ObservedTempoStep) Valid(tick, tempo int64) bool {
	if tick < 0 || s.AfterNanos < 0 || s.AfterNanos > 350_000_000 || s.Tick < tick || tempo < 20000 || tempo > 300000 || s.BPMMilli < 20000 || s.BPMMilli > 300000 {
		return false
	}
	expected := float64(s.AfterNanos) * float64(tempo) * 960 / 60_000_000_000_000
	return math.Abs(float64(s.Tick-tick)-expected) <= 1.01
}

// ObservedAdvance measures ticks after an observation, stepping tempo at the
// renderer's boundary. Callers bound elapsed independently for their clock.
func ObservedAdvance(tick, tempo, elapsed int64, step ObservedTempoStep) float64 {
	elapsed = max(0, elapsed)
	if step.Valid(tick, tempo) && elapsed >= step.AfterNanos {
		return float64(step.Tick-tick) + float64(elapsed-step.AfterNanos)*float64(step.BPMMilli)*960/60_000_000_000_000
	}
	return float64(elapsed) * float64(tempo) * 960 / 60_000_000_000_000
}
