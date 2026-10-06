// Package midi adapts MIDI controller resolutions to the kernel command ABI.
// Device discovery and MIDI 1.0 byte or MIDI 2.0 UMP transport belong to hosts.
package midi

import (
	"math"

	"m31labs.dev/cicada/kernel/cmd"
)

// Expression retains MIDI 2.0's unsigned 32-bit pressure and timbre until the
// command boundary. Pitch is already in cents so an input adapter can apply
// its negotiated per-note pitch sensitivity independently of the engine.
type Expression struct {
	NoteID     uint16
	PitchCents float64
	Pressure   uint32
	Timbre     uint32
}

// Command normalizes controllers directly to float32, preserving changes
// smaller than a MIDI 1.0 seven-bit controller step. Values round only once.
func (e Expression) Command(track, tracks uint8, tick int64) (cmd.Command, error) {
	if math.IsNaN(e.PitchCents) || math.IsInf(e.PitchCents, 0) || e.PitchCents < -9600 || e.PitchCents > 9600 {
		return cmd.Command{}, cmd.Error("MIDI pitch cents must be finite and in -9600 to 9600")
	}
	c := cmd.Command{
		Op: cmd.OpNoteExpression, Track: track, Index: e.NoteID, Tick: tick,
		Arg0: math.Float32bits(float32(e.PitchCents)),
		Arg1: math.Float32bits(float32(float64(e.Pressure) / float64(math.MaxUint32))),
		Pad:  math.Float32bits(float32(float64(e.Timbre) / float64(math.MaxUint32))),
	}
	return c, c.Validate(tracks)
}

// PitchBendCents converts a MIDI 2.0 unsigned pitch-bend value: 0x80000000 is
// neutral, zero reaches the negative limit, and 0xffffffff the positive limit.
func PitchBendCents(value uint32, rangeCents float64) (float64, error) {
	if math.IsNaN(rangeCents) || math.IsInf(rangeCents, 0) || rangeCents < 0 || rangeCents > 9600 {
		return 0, cmd.Error("MIDI pitch sensitivity must be finite and in 0 to 9600 cents")
	}
	if value < 0x80000000 {
		return -float64(0x80000000-value) / 0x80000000 * rangeCents, nil
	}
	return float64(value-0x80000000) / 0x7fffffff * rangeCents, nil
}
