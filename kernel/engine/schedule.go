package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

// ScheduleEvent is an immutable action in absolute musical time. EndTick is
// exclusive. The engine owns the cursor; hosts never advance arrangements.
type ScheduleEvent struct {
	Tick, EndTick int64
	Scene         uint16
	Kind          ScheduleKind
	Track         uint8
	Index         uint16
	ID            uint32
}

type scheduleInterval struct {
	Event  int
	MaxEnd int64
}

// The balanced interval index prunes expired subtrees during seeks. Its shape
// and storage are prepared once, and active placements restore in event order.
func (e *Engine) indexSchedule() {
	for i, v := range e.schedule {
		if v.Kind == SchedulePattern || v.Kind == ScheduleClip {
			e.scheduleIntervals = append(e.scheduleIntervals, scheduleInterval{Event: i})
		}
	}
	var build func(int, int) int64
	build = func(lo, hi int) int64 {
		if lo >= hi {
			return 0
		}
		mid := (lo + hi) / 2
		end := max(e.schedule[e.scheduleIntervals[mid].Event].EndTick, build(lo, mid), build(mid+1, hi))
		e.scheduleIntervals[mid].MaxEnd = end
		return end
	}
	build(0, len(e.scheduleIntervals))
}

func (e *Engine) restoreScheduleIntervals(lo, hi int, localTick, tick int64) {
	if lo >= hi {
		return
	}
	mid := (lo + hi) / 2
	interval := e.scheduleIntervals[mid]
	if interval.MaxEnd <= localTick {
		return
	}
	event := e.schedule[interval.Event]
	e.restoreScheduleIntervals(lo, mid, localTick, tick)
	if event.Tick <= localTick {
		if event.EndTick > localTick {
			e.applyScheduleEvent(event, tick)
		}
		e.restoreScheduleIntervals(mid+1, hi, localTick, tick)
	}
}

func (e *Engine) validateSchedule() error {
	starts := make(map[uint32]ScheduleEvent)
	ends := make(map[uint32]bool)
	var scenes, activeClips int
	var previous int64
	for i, v := range e.schedule {
		if v.Tick < 0 || i > 0 && v.Tick < previous || v.EndTick < v.Tick || v.EndTick > 1<<53-1 || v.Kind > ScheduleClipEnd {
			return Error("schedule event is out of range")
		}
		previous = v.Tick
		e.scheduleDuration = max(e.scheduleDuration, v.Tick, v.EndTick)
		if v.Kind == ScheduleScene {
			scenes++
			if int(v.Scene) >= len(e.scenes) || v.EndTick <= v.Tick {
				return Error("schedule scene is invalid")
			}
			continue
		}
		e.placementSchedule = true
		if int(v.Track) >= e.tracks || v.ID == 0 {
			return Error("placement track or identity is invalid")
		}
		switch v.Kind {
		case SchedulePattern:
			if v.Index >= 16 || e.patterns[v.Track].slots[v.Index].Len == 0 || v.Tick%seq.TicksPerStep != 0 || v.EndTick <= v.Tick || e.voices[v.Track].kind == VoiceAudio {
				return Error("placement pattern is invalid")
			}
		case ScheduleClip:
			if int(v.Index) >= len(e.clipTemplates) || e.voices[v.Track].kind != VoiceAudio || v.EndTick <= v.Tick {
				return Error("placement clip is invalid")
			}
		}
		if v.Kind == SchedulePattern || v.Kind == ScheduleClip {
			if _, exists := starts[v.ID]; exists {
				return Error("duplicate placement identity")
			}
			starts[v.ID] = v
			if v.Kind == ScheduleClip {
				activeClips++
				if activeClips > e.clipLimit {
					return Error("schedule exceeds clip voice capacity")
				}
			}
		} else {
			start, exists := starts[v.ID]
			if !exists || ends[v.ID] || start.Track != v.Track || start.EndTick != v.Tick || v.Kind != start.Kind+1 || v.EndTick != v.Tick {
				return Error("unmatched placement release")
			}
			ends[v.ID] = true
			if v.Kind == ScheduleClipEnd {
				activeClips--
			}
		}
	}
	if e.placementSchedule && (scenes > 0 || len(starts) != len(ends)) {
		return Error("invalid placement schedule authority or releases")
	}
	if !e.placementSchedule {
		var end int64
		for _, v := range e.schedule {
			if v.Tick != end {
				return Error("song schedule must be contiguous")
			}
			end = v.EndTick
		}
	}
	e.indexSchedule()
	return nil
}

func (e *Engine) startPlacementSchedule() {
	if e.scheduleDuration <= 0 {
		return
	}
	tick := e.transport.Tick()
	if tick >= e.scheduleDuration && !e.loopSong {
		_ = e.transport.SeekTick(0)
		tick = 0
	}
	e.cycleStart = 0
	if e.loopSong {
		e.cycleStart = tick / e.scheduleDuration * e.scheduleDuration
	}
	e.songMode = true
	e.songIndex = -1
	e.resetClips()
	for track := 0; track < e.tracks; track++ {
		e.resetVoice(track)
		e.patterns[track].active = -1
		e.patterns[track].eventCount = 0
		e.patterns[track].eventIndex = 0
		e.placementID[track] = 0
	}
	localTick := tick - e.cycleStart
	lo, hi := 0, len(e.schedule)
	for lo < hi {
		mid := (lo + hi) / 2
		if e.schedule[mid].Tick <= localTick {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	e.songIndex = lo - 1
	e.restoreScheduleIntervals(0, len(e.scheduleIntervals), localTick, tick)
	e.setNextScheduleTick()
}
func (e *Engine) setNextScheduleTick() {
	e.songEndTick = e.cycleStart + e.scheduleDuration
	if e.songIndex+1 < len(e.schedule) {
		e.songEndTick = e.cycleStart + e.schedule[e.songIndex+1].Tick
	}
}
func (e *Engine) advancePlacementSchedule() {
	if !e.songMode || !e.transport.Playing() {
		return
	}
	tick := e.transport.Tick()
	for tick >= e.songEndTick {
		if e.songIndex+1 == len(e.schedule) {
			if !e.loopSong {
				e.songMode = false
				e.transport.Stop()
				for t := 0; t < e.tracks; t++ {
					e.noteOff(t, 0xffff)
				}
				e.resetClips()
				return
			}
			e.cycleStart += e.scheduleDuration
			e.songIndex = -1
		}
		e.songIndex++
		e.applyScheduleEvent(e.schedule[e.songIndex], tick)
		if e.faulted {
			return
		}
		e.setNextScheduleTick()
	}
}
func (e *Engine) applyScheduleEvent(v ScheduleEvent, tick int64) {
	track := int(v.Track)
	switch v.Kind {
	case SchedulePattern:
		e.placementID[track] = v.ID
		e.applyPatternCommand(cmd.Command{Op: cmd.OpSelectPattern, Track: v.Track, Index: v.Index, Arg1: 1})
		e.patterns[track].startTick = v.Tick + e.cycleStart
		e.scheduleTrack(track)
	case SchedulePatternEnd:
		if e.placementID[track] != v.ID {
			return
		}
		e.noteOff(track, 0xffff)
		p := &e.patterns[track]
		p.active = -1
		p.chainArmed = false
		p.playingNote = 0
		p.heldValid = false
		p.eventCount = 0
		p.eventIndex = 0
	case ScheduleClip:
		clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.transport.BPMMilli()}
		elapsed := clock.SampleAtTick(tick) - clock.SampleAtTick(v.Tick+e.cycleStart)
		e.startClip(track, v.Index, int(v.ID), max(0, elapsed))
	case ScheduleClipEnd:
		for i := range e.clipVoices {
			p := &e.clipVoices[i]
			if p.active && p.id == int(v.ID) {
				p.voice.Reset()
				p.active = false
			}
		}
	}
}

type ScheduleKind uint8

const (
	ScheduleScene ScheduleKind = iota
	SchedulePattern
	SchedulePatternEnd
	ScheduleClip
	ScheduleClipEnd
)

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
