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
	drumSlots   *[16][drum.LaneCount]seq.Pattern
	active      int8 // -1 until a slot is selected
	startStep   int64
	generation  uint32
	playingNote int64
	playingGen  uint32
	slideFrom   int64
	slideAt     int64
	forceOff    seq.Event
	forceGen    uint32
	forceValid  bool
	held        seq.Pattern
	heldSlot    uint8
	heldStart   int64
	heldGen     uint32
	heldValid   bool
	events      [256]patternEvent
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
		p.slideFrom, p.slideAt = 0, 0
		p.forceValid = false
		if p.playingNote != 0 && p.playingGen == p.generation && p.active >= 0 && e.switchSlideTarget(track, slot, e.transport.Tick(), c.Arg1 == 1) {
			p.slideFrom = p.playingNote
			p.slideAt = e.switchOnsetSample(track, slot, e.transport.Tick())
		} else if release, ok := e.normalSlideRelease(track, e.transport.Tick(), e.transport.Clock()); ok && p.playingNote == release.NoteID && release.Sample >= e.transport.Sample() {
			p.forceOff, p.forceGen, p.forceValid = release, p.generation, true
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
		if err != nil {
			e.fault(15)
			return
		}
		if p.drumSlots != nil {
			if step.Note >= uint8(drum.LaneCount) || step.Tie {
				e.fault(15)
				return
			}
			lane := drum.Lane(step.Note)
			lanePattern := p.drumSlots[slot][lane]
			lanePattern.Steps[c.Index] = c.Arg0
			if lanePattern.Validate() != nil {
				e.fault(15)
				return
			}
			p.drumSlots[slot][lane] = lanePattern
			if p.active == int8(slot) && e.renderFrames > 0 {
				e.scheduleTrack(track)
			}
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
	if p.drumSlots != nil {
		for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
			lanePattern := p.drumSlots[slot][lane]
			lanePattern.Len = updated.Len
			lanePattern.SwingPermille = updated.SwingPermille
			lanePattern.Transpose = updated.Transpose
			lanePattern.GatePercent = updated.GatePercent
			p.drumSlots[slot][lane] = lanePattern
		}
	}
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
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				n, overflow = seq.EventsOffsetInBlock(&p.drumSlots[p.active][lane], clock, uint8(track), uint8(p.active), p.startStep, startSample, frames, e.eventScratch[:])
				if overflow || p.eventCount+n > len(p.events) {
					e.fault(16)
					return
				}
				for _, event := range e.eventScratch[:n] {
					p.events[p.eventCount] = patternEvent{event: event, generation: p.generation}
					p.eventCount++
				}
			}
		} else {
			n, overflow = seq.EventsWithGatesOffsetInBlock(&p.slots[p.active], clock, uint8(track), uint8(p.active), p.startStep, startSample, frames, e.eventScratch[:])
			if overflow || p.eventCount+n > len(p.events) {
				e.fault(16)
				return
			}
			for _, event := range e.eventScratch[:n] {
				if event.Kind == seq.NoteOn && event.Sample == p.slideAt && p.playingNote == p.slideFrom && p.slideFrom != 0 {
					event.Slide = true
				}
				p.events[p.eventCount] = patternEvent{event: event, generation: p.generation}
				p.eventCount++
			}
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
	if p.forceValid && p.playingNote == p.forceOff.NoteID && p.forceOff.Sample >= startSample && p.forceOff.Sample < startSample+int64(frames) {
		if p.eventCount == len(p.events) {
			e.fault(16)
			return
		}
		p.events[p.eventCount] = patternEvent{event: p.forceOff, generation: p.forceGen}
		p.eventCount++
	}
	e.scheduleSwitchRelease(track, clock, startSample, frames)
	for i := 1; i < p.eventCount; i++ {
		current := p.events[i]
		j := i
		for j > 0 && patternEventBefore(current, p.events[j-1], p.drumSlots != nil) {
			p.events[j] = p.events[j-1]
			j--
		}
		p.events[j] = current
	}
}

func patternEventBefore(a, b patternEvent, isDrum bool) bool {
	if a.event.Sample != b.event.Sample {
		return a.event.Sample < b.event.Sample
	}
	if a.event.Kind != b.event.Kind {
		return a.event.Kind == seq.NoteOff
	}
	if isDrum && a.event.Note != b.event.Note {
		priority := func(lane uint8) uint8 {
			if lane == uint8(drum.CH) {
				return uint8(drum.OH)
			}
			if lane == uint8(drum.OH) {
				return uint8(drum.CH)
			}
			return lane
		}
		return priority(a.event.Note) < priority(b.event.Note)
	}
	return a.event.NoteID < b.event.NoteID
}

func (e *Engine) switchOnsetSample(track, slot int, tick int64) int64 {
	step := tick / seq.TicksPerStep
	if step&1 != 0 {
		tick += seq.SwingDelayTicks(e.patterns[track].slots[slot].SwingPermille)
	}
	return e.transport.Clock().SampleAtTick(tick)
}

func (e *Engine) switchSlideTarget(track, slot int, tick int64, restart bool) bool {
	p := &e.patterns[track]
	if p.playingNote == 0 || p.playingGen != p.generation || (p.playingNote-1)/8+1 != tick/seq.TicksPerStep {
		return false
	}
	return e.switchWouldSlide(track, slot, tick, restart)
}

func (e *Engine) switchWouldSlide(track, slot int, tick int64, restart bool) bool {
	p := &e.patterns[track]
	if p.active < 0 || slot < 0 || slot >= len(p.slots) || tick%seq.TicksPerStep != 0 {
		return false
	}
	targetStep := tick / seq.TicksPerStep
	sourceStep := targetStep - 1
	if sourceStep < p.startStep {
		return false
	}
	source := &p.slots[p.active]
	sourceIndex := (sourceStep - p.startStep) % int64(source.Len)
	old, err := seq.UnpackStep(source.Steps[sourceIndex])
	if err != nil || !old.Gate || !old.Slide || !seq.ProbabilityHit(old.Probability, source.Seed, uint8(track), uint8(p.active), (sourceStep-p.startStep)/int64(source.Len), uint8(sourceIndex)) {
		return false
	}
	target := &p.slots[slot]
	localStep := targetStep
	if restart {
		localStep = 0
	}
	targetIndex := uint8(localStep % int64(target.Len))
	next, err := seq.UnpackStep(target.Steps[targetIndex])
	return err == nil && next.Gate && !next.Tie && seq.ProbabilityHit(next.Probability, target.Seed, uint8(track), uint8(slot), localStep/int64(target.Len), targetIndex)
}

func (e *Engine) scheduleSwitchRelease(track int, clock seq.Clock, startSample int64, frames int) {
	p := &e.patterns[track]
	if p.active < 0 || p.drumSlots != nil {
		return
	}
	for i := 0; i < e.pendingLen; i++ {
		c := e.pending[i]
		slot, restart := -1, false
		switch c.Op {
		case cmd.OpSelectPattern:
			if int(c.Track) != track {
				continue
			}
			slot, restart = int(c.Index), c.Arg1 == 1
		case cmd.OpLaunchScene:
			if int(c.Index) >= len(e.scenes) {
				continue
			}
			binding := e.scenes[c.Index].Track[track]
			if binding.Mode != SceneSlot || p.active == int8(binding.Slot) {
				continue
			}
			slot = int(binding.Slot)
		default:
			continue
		}
		e.scheduleNormalRelease(track, slot, c.Tick, restart, clock, startSample, frames)
	}
	if e.songMode && (e.songIndex+1 < len(e.song) || e.loopSong) {
		next := (e.songIndex + 1) % len(e.song)
		binding := e.scenes[e.song[next].Scene].Track[track]
		if binding.Mode == SceneSlot && p.active != int8(binding.Slot) {
			e.scheduleNormalRelease(track, int(binding.Slot), e.songEndTick, false, clock, startSample, frames)
		}
	}
}

func (e *Engine) scheduleNormalRelease(track, slot int, switchTick int64, restart bool, clock seq.Clock, startSample int64, frames int) {
	p := &e.patterns[track]
	if e.switchWouldSlide(track, slot, switchTick, restart) {
		return
	}
	release, ok := e.normalSlideRelease(track, switchTick, clock)
	if !ok {
		return
	}
	offSample := release.Sample
	if offSample < startSample && p.playingNote == release.NoteID && startSample < clock.SampleAtTick(switchTick) {
		offSample = startSample
		release.Sample = offSample
		release.Tick = clock.TickAtSample(startSample)
	}
	if offSample < startSample || offSample >= startSample+int64(frames) {
		return
	}
	if p.eventCount == len(p.events) {
		e.fault(16)
		return
	}
	p.events[p.eventCount] = patternEvent{event: release, generation: p.generation}
	p.eventCount++
}

func (e *Engine) normalSlideRelease(track int, switchTick int64, clock seq.Clock) (seq.Event, bool) {
	p := &e.patterns[track]
	if p.active < 0 || switchTick < seq.TicksPerStep || switchTick%seq.TicksPerStep != 0 {
		return seq.Event{}, false
	}
	step := switchTick/seq.TicksPerStep - 1
	local := step - p.startStep
	source := &p.slots[p.active]
	if local < 0 {
		return seq.Event{}, false
	}
	index := uint8(local % int64(source.Len))
	old, err := seq.UnpackStep(source.Steps[index])
	if err != nil || !old.Gate || !old.Slide || !seq.ProbabilityHit(old.Probability, source.Seed, uint8(track), uint8(p.active), local/int64(source.Len), index) {
		return seq.Event{}, false
	}
	startTick := step * seq.TicksPerStep
	endTick := switchTick
	if step&1 != 0 {
		startTick += seq.SwingDelayTicks(source.SwingPermille)
	}
	if (step+1)&1 != 0 {
		endTick += seq.SwingDelayTicks(source.SwingPermille)
	}
	last := old.Ratchet - 1
	onset := seq.RatchetTick(startTick, endTick, old.Ratchet, last)
	gateTicks := (endTick - onset) * int64(source.GatePercent) / 100
	if gateTicks < 30 {
		gateTicks = 30
	}
	offTick := onset + gateTicks
	offSample := clock.SampleAtTick(offTick)
	noteID := step*8 + int64(last) + 1
	return seq.Event{Kind: seq.NoteOff, NoteID: noteID, Tick: offTick, Sample: offSample, Track: uint8(track), Slot: uint8(p.active), StepIndex: index}, true
}

// pendingSwitchSlides decides before the outgoing gate closes. The scene or
// pattern command is already quantized in the pending queue by this point.
func (e *Engine) pendingSwitchSlides(track int, off seq.Event) bool {
	p := &e.patterns[track]
	if p.active < 0 || p.playingNote != off.NoteID {
		return false
	}
	switchTick := ((off.NoteID-1)/8 + 1) * seq.TicksPerStep
	for i := 0; i < e.pendingLen; i++ {
		c := e.pending[i]
		if c.Tick != switchTick {
			continue
		}
		switch c.Op {
		case cmd.OpSelectPattern:
			if int(c.Track) == track {
				return e.switchSlideTarget(track, int(c.Index), switchTick, c.Arg1 == 1)
			}
		case cmd.OpLaunchScene:
			if int(c.Index) < len(e.scenes) {
				binding := e.scenes[c.Index].Track[track]
				if binding.Mode == SceneSlot && p.active != int8(binding.Slot) {
					return e.switchSlideTarget(track, int(binding.Slot), switchTick, false)
				}
			}
		}
	}
	if e.songMode && e.songEndTick == switchTick && (e.songIndex+1 < len(e.song) || e.loopSong) {
		next := (e.songIndex + 1) % len(e.song)
		binding := e.scenes[e.song[next].Scene].Track[track]
		return binding.Mode == SceneSlot && p.active != int8(binding.Slot) && e.switchSlideTarget(track, int(binding.Slot), switchTick, false)
	}
	return false
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
					if p.slideFrom == item.event.NoteID && sample <= p.slideAt || e.pendingSwitchSlides(track, item.event) {
						continue
					}
					e.noteOff(track, 0xffff)
					p.playingNote = 0
					p.heldValid = false
					p.forceValid = false
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
				p.slideFrom, p.slideAt = 0, 0
				p.forceValid = false
			}
			e.emit(cmd.Message{Kind: cmd.NoteOn, Track: uint8(track), A: uint16(event.Note), Tick: event.Tick})
		}
	}
}
