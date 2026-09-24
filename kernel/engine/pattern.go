package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

type patternEvent struct {
	event      seq.Event
	generation uint32
}

type patternTrack struct {
	slots       [16]seq.Pattern
	active      int8 // -1 until a slot is selected
	startStep   int64
	generation  uint32
	playingNote int64
	playingGen  uint32
	held        seq.Pattern
	heldSlot    uint8
	heldStart   int64
	heldGen     uint32
	heldValid   bool
	events      [128]patternEvent
	eventCount  int
	eventIndex  int
}

func (e *Engine) applyPatternCommand(c cmd.Command) {
	track := int(c.Track)
	p := &e.patterns[track]
	slot := int(c.Arg1)
	if c.Op == cmd.OpSelectPattern {
		if e.voices[track].kind == VoiceOff {
			e.fault(9)
			return
		}
		slot = int(c.Index)
		length := p.slots[slot].Len
		if p.active >= 0 {
			length = p.slots[p.active].Len
		}
		at, err := seq.QuantizeTick(e.transport.Tick(), cmd.Quantize(c.Arg0), length)
		if c.Arg0 == 3 && p.active >= 0 && p.startStep != 0 {
			patternTicks := int64(length) * seq.TicksPerStep
			startTick := p.startStep * seq.TicksPerStep
			if e.transport.Tick() <= startTick {
				at = startTick
			} else {
				elapsed := e.transport.Tick() - startTick
				at = startTick + ((elapsed+patternTicks-1)/patternTicks)*patternTicks
			}
		}
		if err != nil {
			e.fault(14)
			return
		}
		if at > e.transport.Tick() {
			if e.pendingLen == len(e.pending) {
				e.fault(5)
				return
			}
			c.Tick, c.Arg0 = at, 0
			e.pending[e.pendingLen] = c
			e.pendingLen++
			return
		}
		if p.playingNote != 0 && p.playingGen == p.generation && p.active >= 0 {
			p.held = p.slots[p.active]
			p.heldSlot = uint8(p.active)
			p.heldStart = p.startStep
			p.heldGen = p.generation
			p.heldValid = true
		}
		p.active = int8(slot)
		p.generation++
		p.startStep = 0
		if c.Arg1 == 1 {
			p.startStep = (e.transport.Tick() + seq.TicksPerStep - 1) / seq.TicksPerStep
		}
		if e.renderFrames > 0 {
			e.scheduleTrack(track)
		}
		e.emit(cmd.Message{Kind: cmd.Switched, Track: c.Track, A: uint16(slot), Tick: e.transport.Tick()})
		return
	}
	updated := p.slots[slot]
	switch c.Op {
	case cmd.OpSetStep:
		step, err := seq.UnpackStep(c.Arg0)
		if err != nil || e.voices[track].kind == VoiceDrums && step.Gate && !step.Tie && step.Note >= uint8(drum.LaneCount) {
			e.fault(15)
			return
		}
		updated.Steps[c.Index] = c.Arg0
	case cmd.OpSetPatternLen:
		updated.Len = uint8(c.Index)
	case cmd.OpSetPatternMeta:
		updated.SwingPermille = uint16(c.Arg0)
		updated.Transpose = int8(int16(c.Arg0 >> 16))
		if e.voices[track].kind == VoiceDrums && updated.Transpose != 0 {
			e.fault(15)
			return
		}
	}
	if updated.Validate() != nil {
		e.fault(15)
		return
	}
	if p.active == int8(slot) && p.playingNote != 0 && p.playingGen == p.generation {
		p.held = p.slots[slot]
		p.heldSlot = uint8(slot)
		p.heldStart = p.startStep
		p.heldGen = p.generation
		p.heldValid = true
		p.generation++
	}
	p.slots[slot] = updated
	if p.active == int8(slot) && e.renderFrames > 0 {
		e.scheduleTrack(track)
	}
}

func (e *Engine) scheduleAll() {
	for track := 0; track < e.tracks && !e.faulted; track++ {
		e.scheduleTrack(track)
	}
}

func (e *Engine) scheduleTrack(track int) {
	p := &e.patterns[track]
	p.eventCount, p.eventIndex = 0, 0
	frames := e.renderFrames - e.renderFrame
	if !e.transport.Playing() || frames < 1 {
		return
	}
	clock := e.transport.Clock()
	startSample := e.transport.Sample()
	if p.active >= 0 {
		var n int
		var overflow bool
		if e.voices[track].kind == VoiceDrums {
			n, overflow = seq.EventsOffsetInBlock(&p.slots[p.active], clock, uint8(track), uint8(p.active), p.startStep, startSample, frames, e.eventScratch[:])
		} else {
			n, overflow = seq.EventsWithGatesOffsetInBlock(&p.slots[p.active], clock, uint8(track), uint8(p.active), p.startStep, startSample, frames, e.eventScratch[:])
		}
		if overflow {
			e.fault(16)
			return
		}
		for _, event := range e.eventScratch[:n] {
			p.events[p.eventCount] = patternEvent{event: event, generation: p.generation}
			p.eventCount++
		}
	}
	if p.heldValid && p.playingNote != 0 {
		n, overflow := seq.EventsWithGatesOffsetInBlock(&p.held, clock, uint8(track), p.heldSlot, p.heldStart, startSample, frames, e.eventScratch[:])
		if overflow {
			e.fault(16)
			return
		}
		for _, event := range e.eventScratch[:n] {
			if event.Kind != seq.NoteOff || event.NoteID != p.playingNote {
				continue
			}
			if p.eventCount == len(p.events) {
				e.fault(16)
				return
			}
			p.events[p.eventCount] = patternEvent{event: event, generation: p.heldGen}
			p.eventCount++
		}
	}
	for i := 1; i < p.eventCount; i++ {
		current := p.events[i]
		j := i
		for j > 0 && patternEventBefore(current, p.events[j-1]) {
			p.events[j] = p.events[j-1]
			j--
		}
		p.events[j] = current
	}
}

func patternEventBefore(a, b patternEvent) bool {
	if a.event.Sample != b.event.Sample {
		return a.event.Sample < b.event.Sample
	}
	if a.event.Kind != b.event.Kind {
		return a.event.Kind == seq.NoteOff
	}
	return a.event.NoteID < b.event.NoteID
}

func (e *Engine) processPatternEvents(kind seq.EventKind) {
	if !e.transport.Playing() || e.faulted {
		return
	}
	sample := e.transport.Sample()
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		for p.eventIndex < p.eventCount {
			item := p.events[p.eventIndex]
			if item.event.Sample > sample || item.event.Sample == sample && item.event.Kind != kind {
				break
			}
			p.eventIndex++
			if item.event.Sample < sample {
				continue
			}
			if kind == seq.NoteOff {
				if p.playingNote == item.event.NoteID && p.playingGen == item.generation {
					e.noteOff(track, 0xffff)
					p.playingNote = 0
					p.heldValid = false
					e.emit(cmd.Message{Kind: cmd.NoteOff, Track: uint8(track), Tick: item.event.Tick})
				}
				continue
			}
			if item.generation != p.generation {
				continue
			}
			event := item.event
			switch e.voices[track].kind {
			case VoiceAcid:
				e.voices[track].acid.NoteOn(event.Note, event.Accent, event.Slide, event.Velocity)
			case VoiceGraph:
				e.voices[track].graph.NoteOn(event.Note, event.Velocity, event.Slide)
			case VoiceDrums:
				if event.Note >= uint8(drum.LaneCount) {
					e.fault(15)
					return
				}
				e.voices[track].drums.Hit(drum.Lane(event.Note), event.Velocity, event.Accent)
			default:
				e.fault(9)
				return
			}
			if e.voices[track].kind != VoiceDrums {
				p.playingNote, p.playingGen = event.NoteID, item.generation
				p.heldValid = false
			}
			e.emit(cmd.Message{Kind: cmd.NoteOn, Track: uint8(track), A: uint16(event.Note), Tick: event.Tick})
		}
	}
}
