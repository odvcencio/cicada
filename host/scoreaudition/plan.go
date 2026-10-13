package scoreaudition

import (
	"fmt"
	"sort"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// placement is one pattern playing on one track over a span of ticks.
type placement struct {
	track      int
	slot       int
	start, end int64
	offset     int64 // tick of the pattern's first cell
}

// buildPlacements expands the song or arrangement into pattern placements.
// Scene bindings follow the engine: an omitted binding keeps the track's
// current pattern, and a pattern that continues across scenes keeps its phase.
func buildPlacements(p *project.Project, cfg engine.Config, events []engine.ScheduleEvent) ([]placement, error) {
	var out []placement
	if p.Arrange != nil {
		for _, e := range events {
			if e.Kind == engine.SchedulePattern {
				out = append(out, placement{track: int(e.Track), slot: int(e.Index), start: e.Tick, end: e.EndTick, offset: e.Tick})
			}
		}
		return out, nil
	}
	type state struct {
		slot   int
		offset int64
	}
	current := make([]state, len(p.Tracks))
	for i := range current {
		current[i].slot = -1
	}
	for _, e := range events {
		if e.Kind != engine.ScheduleScene {
			continue
		}
		if int(e.Scene) >= len(cfg.Scenes) {
			return nil, fmt.Errorf("scene index %d out of range", e.Scene)
		}
		for ti := range p.Tracks {
			b := cfg.Scenes[e.Scene].Track[ti]
			switch b.Mode {
			case engine.SceneOff, engine.SceneClip:
				current[ti].slot = -1
			case engine.SceneSlot:
				if current[ti].slot != int(b.Slot) {
					current[ti] = state{slot: int(b.Slot), offset: e.Tick}
				}
			}
			if current[ti].slot >= 0 {
				out = append(out, placement{track: ti, slot: current[ti].slot, start: e.Tick, end: e.EndTick, offset: current[ti].offset})
			}
		}
	}
	return out, nil
}

func pitchedKind(k engine.VoiceKind) bool {
	switch k {
	case engine.VoiceOff, engine.VoiceDrums, engine.VoiceAudio:
		return false
	}
	return true
}

// writtenSets returns, for each analyzed bar, the pitch classes of the note
// onsets that pitched tracks play in it.
func writtenSets(p *project.Project, cfg engine.Config, places []placement, fromBar, bars int) ([][12]bool, error) {
	sets := make([][12]bool, bars)
	clock, err := seq.NewClock(48000, int64(p.TempoMilli))
	if err != nil {
		return nil, err
	}
	lo, hi := int64(fromBar)*seq.TicksPerBar, int64(fromBar+bars)*seq.TicksPerBar
	for _, pl := range places {
		if pl.track >= len(p.Tracks) || !pitchedKind(cfg.Track[pl.track].Kind) || pl.end <= lo || pl.start >= hi {
			continue
		}
		if id := p.Tracks[pl.track].Slots[pl.slot]; id != nil {
			if isDrumPattern(p, *id) {
				continue
			}
		}
		pat := cfg.Patterns[pl.track].Slots[pl.slot]
		if pat.Len == 0 {
			continue
		}
		start, end := max(pl.start, lo), min(pl.end, hi)
		startSample := clock.SampleAtTick(start)
		frames := int(clock.SampleAtTick(end) - startSample)
		buf := make([]seq.Event, 256)
		for {
			n, overflow := seq.EventsAtTickInBlock(&pat, clock, uint8(pl.track), uint8(pl.slot), pl.offset, startSample, frames, buf)
			if overflow {
				buf = make([]seq.Event, len(buf)*4)
				continue
			}
			for _, ev := range buf[:n] {
				if ev.Kind != seq.NoteOn {
					continue
				}
				bar := int(ev.Tick/seq.TicksPerBar) - fromBar
				if bar < 0 || bar >= bars {
					continue
				}
				if ev.NoteCount > 0 {
					for _, note := range ev.Notes[:ev.NoteCount] {
						sets[bar][note%12] = true
					}
				} else {
					sets[bar][ev.Note%12] = true
				}
			}
			break
		}
	}
	return sets, nil
}

func isDrumPattern(p *project.Project, id string) bool {
	for _, pat := range p.Patterns {
		if pat.ID == id {
			return pat.Kind == "drums"
		}
	}
	return false
}

// barActivity records which patterns each track plays in each analyzed bar.
type barActivity struct {
	active  []bool // by bar
	pattern []*seq.Pattern
}

func trackActivity(cfg engine.Config, places []placement, track, fromBar, bars int) barActivity {
	a := barActivity{active: make([]bool, bars), pattern: make([]*seq.Pattern, bars)}
	sorted := append([]placement(nil), places...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	for _, pl := range sorted {
		if pl.track != track {
			continue
		}
		for b := 0; b < bars; b++ {
			tick := int64(fromBar+b) * seq.TicksPerBar
			if tick >= pl.start && tick < pl.end {
				a.active[b] = true
				pat := &cfg.Patterns[track].Slots[pl.slot]
				if pat.Len == 0 && cfg.Patterns[track].Drums != nil {
					pat = &cfg.Patterns[track].Drums[pl.slot][0]
				}
				a.pattern[b] = pat
			}
		}
	}
	return a
}
