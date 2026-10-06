package project

import (
	"m31labs.dev/cicada/kernel/engine"
	"sort"
)

// CompileSchedule is the arrangement lowering used by every render host.
// Stable IDs refer to placement source order; releases precede starts at ties.
func CompileSchedule(p *Project) ([]engine.ScheduleEvent, error) {
	if err := ValidateProject(p); err != nil {
		return nil, err
	}
	if p.Arrange == nil {
		scenes := map[string]uint16{}
		for i, s := range p.Scenes {
			scenes[s.ID] = uint16(i)
		}
		song := make([]engine.SongEntry, len(p.Song))
		for i, s := range p.Song {
			song[i] = engine.SongEntry{Scene: scenes[s.Scene], Bars: s.Bars}
		}
		return engine.LowerSong(song), nil
	}
	tracks := map[string]int{}
	for i, t := range p.Tracks {
		tracks[t.ID] = i
	}
	clips := map[string]uint16{}
	for i, c := range p.Clips {
		clips[c.Name] = uint16(i)
	}
	events := make([]engine.ScheduleEvent, 0, 2*len(p.Arrange.Placements))
	for i, v := range p.Arrange.Placements {
		ti := tracks[v.Track]
		start := engine.ScheduleEvent{Tick: v.AtTick, EndTick: v.AtTick + v.LengthTicks, Track: uint8(ti), ID: uint32(i + 1)}
		end := engine.ScheduleEvent{Tick: start.EndTick, EndTick: start.EndTick, Track: start.Track, ID: start.ID}
		if p.Tracks[ti].Kind == "audio" {
			start.Kind = engine.ScheduleClip
			start.Index = clips[v.Content]
			end.Kind = engine.ScheduleClipEnd
		} else {
			start.Kind = engine.SchedulePattern
			end.Kind = engine.SchedulePatternEnd
			for slot, pattern := range p.Tracks[ti].Slots {
				if pattern != nil && *pattern == v.Content {
					start.Index = uint16(slot)
					break
				}
			}
		}
		events = append(events, start, end)
	}
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		aEnd := a.Kind == engine.SchedulePatternEnd || a.Kind == engine.ScheduleClipEnd
		bEnd := b.Kind == engine.SchedulePatternEnd || b.Kind == engine.ScheduleClipEnd
		return aEnd && !bEnd
	})
	return events, nil
}
