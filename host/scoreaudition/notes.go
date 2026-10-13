package scoreaudition

import (
	"sort"
	"sync"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// loggedNote is one note start the renderer played, with its release.
type loggedNote struct {
	track  int
	slot   uint8
	tick   int64
	end    int64 // release tick; equal to tick for a hit with no release
	off    bool  // a release was tapped
	notes  [4]uint8
	count  uint8
	noteID int64
}

// noteLog collects the render's note events (render.Options.NoteTap).
type noteLog struct {
	mu    sync.Mutex
	notes []loggedNote
	open  map[[2]int64]int // (track, note id) -> index of a note not yet released
	cur   map[int]int      // track -> index of its latest start
}

func (l *noteLog) tap(track int, ev seq.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open == nil {
		l.open, l.cur = map[[2]int64]int{}, map[int]int{}
	}
	key := [2]int64{int64(track), ev.NoteID}
	switch ev.Kind {
	case seq.NoteOn:
		n := loggedNote{track: track, slot: ev.Slot, tick: ev.Tick, end: ev.Tick, noteID: ev.NoteID, notes: ev.Notes, count: ev.NoteCount}
		if n.count == 0 {
			n.notes[0], n.count = ev.Note, 1
		}
		// A new note on a track ends the one it replaces (a slide's source, or
		// a note whose release the engine did not report).
		if i, ok := l.cur[track]; ok && !l.notes[i].off {
			l.notes[i].end, l.notes[i].off = max(l.notes[i].end, ev.Tick), true
			delete(l.open, [2]int64{int64(track), l.notes[i].noteID})
		}
		l.open[key] = len(l.notes)
		l.cur[track] = len(l.notes)
		l.notes = append(l.notes, n)
	case seq.NoteOff:
		if i, ok := l.open[key]; ok {
			if ev.Tick > l.notes[i].end {
				l.notes[i].end = ev.Tick
			}
			l.notes[i].off = true
			delete(l.open, key)
		}
	}
}

func pitchedKind(k engine.VoiceKind) bool {
	switch k {
	case engine.VoiceOff, engine.VoiceDrums, engine.VoiceAudio:
		return false
	}
	return true
}

// writtenSets returns the pitch classes of the pitched tracks' note starts in
// each analyzed bar.
func (l *noteLog) writtenSets(pitched []bool, fromTick int64, bars int) [][12]bool {
	sets := make([][12]bool, bars)
	for _, n := range l.notes {
		bar := int((n.tick - fromTick) / seq.TicksPerBar)
		if n.tick < fromTick || bar >= bars || n.track >= len(pitched) || !pitched[n.track] {
			continue
		}
		for _, note := range n.notes[:n.count] {
			sets[bar][note%12] = true
		}
	}
	return sets
}

type trackSlot struct{ track, slot int }

// slotsUsed lists the (track, pattern slot) pairs that played notes.
func (l *noteLog) slotsUsed() []trackSlot {
	seen := map[trackSlot]bool{}
	var out []trackSlot
	for _, n := range l.notes {
		k := trackSlot{n.track, int(n.slot)}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].track != out[j].track {
			return out[i].track < out[j].track
		}
		return out[i].slot < out[j].slot
	})
	return out
}

// barActivity records, for one track, which analyzed bars its notes touch.
type barActivity struct {
	hasEvents bool
	active    []bool // a note starts in the bar or is still sounding in it
	played    []bool // a note starts in the bar
	pattern   []*seq.Pattern
}

// hasEvents reports which tracks logged any note.
func (l *noteLog) hasEvents(tracks int) []bool {
	out := make([]bool, tracks)
	for _, n := range l.notes {
		if n.track < tracks {
			out[n.track] = true
		}
	}
	return out
}

// activity builds each track's bar activity from the logged notes. A bar is
// active when a note overlaps it, so a placement that starts mid-bar counts
// its first bar. The bar's pattern is the one that played its latest start,
// carried forward through bars of sustain.
//
// A pitched note with no tapped release (a tied chord still held when the
// render stops) lasts to the end of the analyzed range. sounding marks the
// bars where each track's stem has sound; it is merged in so a track is
// active wherever it either played or sounded.
func (l *noteLog) activity(cfg engine.Config, tracks int, fromTick int64, bars int, sustain []bool, sounding [][]bool) []barActivity {
	rangeEnd := fromTick + int64(bars)*seq.TicksPerBar
	out := make([]barActivity, tracks)
	for i := range out {
		out[i] = barActivity{active: make([]bool, bars), played: make([]bool, bars), pattern: make([]*seq.Pattern, bars)}
	}
	for _, n := range l.notes {
		if n.track >= tracks {
			continue
		}
		a := &out[n.track]
		a.hasEvents = true
		pat := patternOf(cfg, n.track, int(n.slot))
		first := int((n.tick - fromTick) / seq.TicksPerBar)
		if n.tick < fromTick {
			first = -1
		}
		end := max(n.end, n.tick)
		if !n.off && n.track < len(sustain) && sustain[n.track] {
			end = max(end, rangeEnd)
		}
		// A note released before the range never reaches it. Integer division
		// truncates toward zero, so without this a release less than a bar
		// before the range would mark the range's first bar active.
		if n.tick < fromTick && end <= fromTick {
			continue
		}
		lastBar := int((end - fromTick) / seq.TicksPerBar)
		// A release exactly on a bar line does not reach into that bar.
		if end > n.tick && (end-fromTick)%seq.TicksPerBar == 0 {
			lastBar--
		}
		if first >= 0 && first < bars {
			a.played[first] = true
			a.pattern[first] = pat
		}
		for b := max(first, 0); b <= lastBar && b < bars; b++ {
			a.active[b] = true
			if a.pattern[b] == nil {
				a.pattern[b] = pat
			}
		}
	}
	for ti := range out {
		for b := 0; b < bars && ti < len(sounding); b++ {
			out[ti].active[b] = out[ti].active[b] || sounding[ti][b]
		}
		var carry *seq.Pattern
		for b := 0; b < bars; b++ {
			if out[ti].pattern[b] != nil {
				carry = out[ti].pattern[b]
			} else if out[ti].active[b] {
				out[ti].pattern[b] = carry
			}
		}
	}
	return out
}

func patternOf(cfg engine.Config, track, slot int) *seq.Pattern {
	if track >= len(cfg.Patterns) || slot >= len(cfg.Patterns[track].Slots) {
		return nil
	}
	pat := &cfg.Patterns[track].Slots[slot]
	if pat.Len == 0 && cfg.Patterns[track].Drums != nil {
		pat = &cfg.Patterns[track].Drums[slot][0]
	}
	return pat
}
