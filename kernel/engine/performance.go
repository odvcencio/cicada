package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

type performedGate struct {
	id         int64
	generation uint32
}

// Storage is admitted once per track, outside the callback. Ordinary step
// patterns keep their existing fixed buffers and rendering path.
type performedTrack struct {
	gates   [128]performedGate
	live    [2]uint64
	mono    performedGate
	chase   bool
	events  []patternEvent
	scratch []seq.Event
}

func (p *patternTrack) eventStorage() []patternEvent {
	if p.performed != nil {
		return p.performed.events
	}
	return p.events[:]
}

func (e *Engine) preparePerformedTrack(track int, pattern *seq.Pattern) error {
	if pattern.Performance == nil {
		return nil
	}
	v := &e.voices[track]
	switch {
	case v.poly != nil:
	case v.kind == VoicePiano || keysEnabled && v.kind == VoiceKeys:
	case v.kind == VoiceAcid || v.kind == VoiceGraph || v.kind == VoiceGraphPoly:
	default:
		return Error("performed notes need a pitched live-note voice")
	}
	for _, note := range pattern.Performance.Notes {
		pitch := int(note.Note) + int(pattern.Transpose)
		if (v.kind == VoicePiano || keysEnabled && v.kind == VoiceKeys) && (pitch < keyboard.MinNote || pitch > keyboard.MaxNote) {
			return Error("performed keyboard notes must stay in MIDI range 21 to 108")
		}
	}
	// Admit enough events for any legal tempo and this configured block size,
	// including both ends of gates across loop boundaries. Dense performances
	// therefore sound the same with small device blocks and larger export blocks.
	maxTicks := (int64(e.maxBlock)*300000*seq.PPQ + int64(60000*e.sampleRate) - 1) / int64(60000*e.sampleRate)
	cycles := (maxTicks+pattern.Performance.LengthTicks-1)/pattern.Performance.LengthTicks + 2
	capacity := max(256, int(cycles)*len(pattern.Performance.Notes)*2+128)
	p := &e.patterns[track]
	if p.performed == nil {
		p.performed = &performedTrack{chase: true}
	}
	if len(p.performed.events) < capacity {
		p.performed.events = make([]patternEvent, capacity)
		p.performed.scratch = make([]seq.Event, capacity)
	}
	return nil
}

func (e *Engine) performedEvent(track int, item patternEvent) {
	p := &e.patterns[track]
	state := p.performed
	if state == nil {
		return
	}
	event := item.event
	gate := &state.gates[event.Note]
	v := &e.voices[track]
	cohort := graph.Cohort{NoteID: event.NoteID, Generation: uint64(item.generation)}
	if event.Kind == seq.NoteOff {
		if gate.id != event.NoteID || gate.generation != item.generation {
			return
		}
		*gate = performedGate{}
		if v.poly != nil {
			v.poly.Release(cohort)
		} else if v.kind == VoiceGraphPoly && state.live[event.Note/64]&(uint64(1)<<(event.Note%64)) == 0 {
			v.legacyPoly.NoteOffNote(event.Note)
		} else if state.live[event.Note/64]&(uint64(1)<<(event.Note%64)) == 0 &&
			(!v.noteActive || v.kind == VoicePiano || keysEnabled && v.kind == VoiceKeys) &&
			(v.kind == VoicePiano || keysEnabled && v.kind == VoiceKeys || state.mono.id == event.NoteID && state.mono.generation == item.generation) {
			e.noteOff(track, uint16(event.Note))
		}
		e.tap.fire(track, event)
		// Fault teardown may itself release gates after a full message ring.
		// The fault is already reported; do not recursively overflow while
		// releasing the remaining performed voices.
		if !e.faulted {
			e.emit(cmd.Message{Kind: cmd.NoteOff, Track: uint8(track), A: uint16(event.Note), Tick: event.Tick})
		}
		return
	}
	if item.generation != p.generation || event.Kind != seq.NoteOn {
		return
	}
	switch {
	case v.poly != nil:
		if _, err := v.poly.NoteOn([4]uint8{event.Note}, 1, event.Velocity, false, cohort); err != nil {
			e.fault(FaultPolyNote)
			return
		}
	case v.kind == VoicePiano:
		if v.piano.NoteOn(event.Note, event.Velocity) != nil {
			e.fault(FaultScheduledNote)
			return
		}
	case keysEnabled && v.kind == VoiceKeys:
		if v.keys.NoteOn(event.Note, event.Velocity) != nil {
			e.fault(FaultScheduledNote)
			return
		}
	case v.kind == VoiceGraph:
		v.graph.NoteOn(event.Note, event.Velocity, false)
		v.graphNote, v.graphHeld = event.Note, true
		v.noteActive = false
	case v.kind == VoiceGraphPoly:
		v.legacyPoly.NoteOn(event.Note, event.Velocity, false)
	case v.kind == VoiceAcid:
		v.acid.NoteOn(event.Note, event.Velocity >= 96, false, event.Velocity)
		v.noteActive = false
	}
	*gate = performedGate{id: event.NoteID, generation: item.generation}
	state.mono = *gate
	e.tap.fire(track, event)
	e.emit(cmd.Message{Kind: cmd.NoteOn, Track: uint8(track), A: uint16(event.Note), Tick: event.Tick})
}

func (e *Engine) releasePerformed(track int, tick int64) {
	state := e.patterns[track].performed
	if state == nil {
		return
	}
	for pitch, gate := range state.gates {
		if gate.id != 0 {
			e.performedEvent(track, patternEvent{generation: gate.generation, event: seq.Event{Kind: seq.NoteOff, NoteID: gate.id, Tick: tick, Track: uint8(track), Note: uint8(pitch)}})
		}
	}
}

func (e *Engine) messageStorage() []cmd.Message {
	if e.performanceMessages != nil {
		return e.performanceMessages
	}
	return e.messages[:]
}

// Reconstruct gates when playback starts within a held note. Ordinary onsets
// at this sample still come from the scheduler, after commands and scene offsets
// have settled. A seek restarts the sound's attack; range renders warm from zero.
func (e *Engine) chasePerformed(track int) {
	p := &e.patterns[track]
	state := p.performed
	if state == nil || !state.chase || p.active < 0 {
		return
	}
	state.chase = false
	pattern := &p.slots[p.active]
	if pattern.Performance == nil {
		return
	}
	clock, sample := e.transport.Clock(), e.transport.Sample()
	iteration := (e.transport.Tick() - p.startTick) / pattern.Performance.LengthTicks
	if iteration < 0 {
		return
	}
	base := p.startTick + iteration*pattern.Performance.LengthTicks
	for ordinal, note := range pattern.Performance.Notes {
		if (base+note.Tick < clock.AnchorTick || clock.SampleAtTick(base+note.Tick) < sample) && clock.SampleAtTick(base+note.EndTick) > sample {
			e.performedEvent(track, patternEvent{generation: p.generation, event: seq.Event{
				Kind: seq.NoteOn, NoteID: iteration*seq.MaxPerformanceNotes + int64(ordinal) + 1,
				Tick: e.transport.Tick(), Sample: sample, Track: uint8(track), Slot: uint8(p.active),
				Note: uint8(int(note.Note) + int(pattern.Transpose)), Velocity: note.Velocity,
			}})
		}
	}
}
