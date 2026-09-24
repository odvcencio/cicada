// Package seq contains Cicada's deterministic, integer-time sequencer core.
// It depends only on math/bits so it can be built for the audio worklet.
package seq

import "math/bits"

const (
	PPQ          int64 = 960
	TicksPerStep int64 = PPQ / 4
	TicksPerBar  int64 = PPQ * 4
	minBPMMilli  int64 = 20_000
	maxBPMMilli  int64 = 300_000
	maxInt64     int64 = 1<<63 - 1
)

// Error is a kernel-local error; the kernel cannot import fmt or errors.
type Error string

func (e Error) Error() string { return string(e) }

// Clock maps ticks to the first sample at or after that tick. It has an
// integer tempo, so mappings are independent of render block size.
type Clock struct {
	SampleRate   int64
	BPMMilli     int64
	AnchorSample int64
	AnchorTick   int64
}

func NewClock(sampleRate int, bpmMilli int64) (Clock, error) {
	if sampleRate != 44_100 && sampleRate != 48_000 && sampleRate != 96_000 {
		return Clock{}, Error("sample rate must be 44100, 48000, or 96000")
	}
	if bpmMilli < minBPMMilli || bpmMilli > maxBPMMilli {
		return Clock{}, Error("tempo must be 20 to 300 BPM")
	}
	return Clock{SampleRate: int64(sampleRate), BPMMilli: bpmMilli}, nil
}

// TickAtSample returns the tick reached at sample. Samples before the anchor
// map to the anchor tick; a host reanchors at a tempo-change boundary.
func (c Clock) TickAtSample(sample int64) int64 {
	if sample <= c.AnchorSample {
		return c.AnchorTick
	}
	den := uint64(60_000 * c.SampleRate)
	q, _ := mulDiv(uint64(sample-c.AnchorSample), uint64(c.BPMMilli*PPQ), den)
	if q > uint64(maxInt64-c.AnchorTick) {
		return maxInt64
	}
	return c.AnchorTick + int64(q)
}

// SampleAtTick returns the first sample whose TickAtSample is at least tick.
// This uses ceiling division. Nearest-sample rounding would violate the
// required tick(sample(t)) == t property for some ticks.
func (c Clock) SampleAtTick(tick int64) int64 {
	if tick <= c.AnchorTick {
		return c.AnchorSample
	}
	q, rem := mulDiv(uint64(tick-c.AnchorTick), uint64(60_000*c.SampleRate), uint64(c.BPMMilli*PPQ))
	if rem != 0 {
		q++
	}
	if q > uint64(maxInt64-c.AnchorSample) {
		return maxInt64
	}
	return c.AnchorSample + int64(q)
}

// Reanchor applies a tempo change at a caller-selected boundary (normally a
// bar). The boundary sample is computed with the old tempo.
func (c Clock) Reanchor(tick, bpmMilli int64) (Clock, error) {
	if bpmMilli < minBPMMilli || bpmMilli > maxBPMMilli {
		return Clock{}, Error("tempo must be 20 to 300 BPM")
	}
	if tick < c.AnchorTick {
		return Clock{}, Error("tempo anchor cannot move backwards")
	}
	c.AnchorSample = c.SampleAtTick(tick)
	c.AnchorTick = tick
	c.BPMMilli = bpmMilli
	return c, nil
}

// mulDiv computes floor(a*b/den) and the remainder with a 128-bit product.
// It saturates the quotient if the result does not fit in uint64.
func mulDiv(a, b, den uint64) (uint64, uint64) {
	hi, lo := bits.Mul64(a, b)
	if hi >= den {
		return ^uint64(0), 0
	}
	return bits.Div64(hi, lo, den)
}
