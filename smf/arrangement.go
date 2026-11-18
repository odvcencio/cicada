package smf

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/project"
)

// Consume the same start/release regions and pattern clocks as playback.
func appendPlacements(file *File, p *project.Project, cfg *project.NoteSchedule, channels []uint8, endTick int64) {
	for _, marker := range p.Arrange.Markers {
		if marker.AtTick < endTick {
			file.Tracks[0].Meta = append(file.Tracks[0].Meta, MetaEvent{Tick: marker.AtTick, Type: 0x06, Data: []byte(marker.ID)})
		}
	}
	for _, event := range cfg.Schedule {
		if event.Kind != engine.SchedulePattern || event.Tick >= endTick {
			continue
		}
		ti, slot := event.Track, uint8(event.Index)
		track := &file.Tracks[int(ti)+1]
		end := min(event.EndTick, endTick)
		origin := event.PatternOrigin(0)
		isDrum := cfg.TrackKinds[ti] == engine.VoiceDrums
		if isDrum {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				appendPlacementPattern(track, &cfg.Patterns[ti].Drums[slot][lane], origin, event.Tick, end, ti, slot, drum.MIDINotes[lane], true, channels[ti])
			}
		} else {
			appendPlacementPattern(track, &cfg.Patterns[ti].Slots[slot], origin, event.Tick, end, ti, slot, 0, false, channels[ti])
		}
	}
}

func appendPlacementPattern(track *TrackChunk, pattern *seq.Pattern, origin, start, end int64, ti, slot, drumPitch uint8, isDrum bool, channel uint8) {
	if pattern.Len == 0 {
		return
	}
	if pattern.Performance != nil {
		if start > origin {
			appendPerformedChase(track, pattern, origin, start, end, ti, channel)
		}
		appendPerformedRange(track, pattern, origin, start, end, end, ti, channel)
		return
	}
	first := len(track.Notes)
	var held heldNote
	grid := pattern.GridTicks()
	for step := (start - origin + grid - 1) / grid; origin+step*grid < end; step++ {
		// Placements play on the scheduled engine, which draws drum
		// humanize with the lane index.
		appendStepOffset(track, pattern, step, origin, ti, ti, slot, drumPitch, isDrum, channel, &held)
	}
	// Swing and ratchets can delay an onset beyond the unswung cell boundary.
	// Remove those outside the region and bound every remaining note-off.
	written := first
	for _, note := range track.Notes[first:] {
		if note.Tick >= end {
			continue
		}
		note.Dur = min(note.Dur, end-note.Tick)
		track.Notes[written] = note
		written++
	}
	track.Notes = track.Notes[:written]
}
