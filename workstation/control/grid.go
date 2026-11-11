package control

import "m31labs.dev/cicada/kernel/seq"

// The browser engine draws one bar and beat grid: the base meter's, read from
// the meter map. Studio does not send the engine a map yet, so it is the zero
// (4/4) map until time-signatures phase 5 sends the song's map as display data.
// These lookups inline on the zero map, so the engine links no multi-segment
// meter code.

// GridBarTicks is the length of one bar of the grid.
func GridBarTicks() int64 {
	var meterMap seq.MeterMap
	return meterMap.BarTicks(0)
}

// GridBeatTicks is the length of one beat of the grid.
func GridBeatTicks() int64 {
	var meterMap seq.MeterMap
	return meterMap.BeatTicks(0)
}

// GridPosition returns the one-based bar, beat and step (a sixteenth note
// within the beat) of a tick of zero or more on the grid.
func GridPosition(tick int64) (bar, beat, step int64) {
	barTicks, beatTicks := GridBarTicks(), GridBeatTicks()
	offset := tick % barTicks
	return tick/barTicks + 1, offset/beatTicks + 1, offset%beatTicks/seq.TicksPerStep + 1
}
