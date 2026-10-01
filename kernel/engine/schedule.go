package engine

import "m31labs.dev/cicada/kernel/seq"

// ScheduleEvent is an immutable action in absolute musical time. EndTick is
// exclusive. The engine owns the cursor; hosts never advance arrangements.
type ScheduleEvent struct {
	Tick, EndTick int64
	Scene         uint16
}

// LowerSong preserves scene entry boundaries, including consecutive entries
// naming the same scene. Patterns keep their existing global-grid phase.
// Call it during compilation, never from Render.
func LowerSong(song []SongEntry) []ScheduleEvent {
	if len(song) == 0 {
		return nil
	}
	events := make([]ScheduleEvent, len(song))
	var tick int64
	for i, entry := range song {
		end := tick + int64(entry.Bars)*seq.TicksPerBar
		events[i] = ScheduleEvent{Tick: tick, EndTick: end, Scene: entry.Scene}
		tick = end
	}
	return events
}
