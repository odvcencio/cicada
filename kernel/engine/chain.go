package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

// OpSetChain at position zero replaces and arms the list. Later positions
// append contiguously or replace an existing entry. The current pattern keeps
// playing until its next end; an idle track starts at the next step boundary.
func (e *Engine) applyChainCommand(c cmd.Command) {
	track := int(c.Track)
	if e.voices[track].kind == VoiceOff {
		e.fault(9)
		return
	}
	p := &e.patterns[track]
	position := uint8(c.Index)
	if position > p.chainLen {
		e.fault(19)
		return
	}
	entry := chainEntry{slot: uint8(c.Arg0), repeats: uint8(c.Arg0 >> 8)}
	if position == 0 {
		p.chainLen = 1
		p.chainNext = 0
		p.chainRepeat = 0
		p.chainDue = chainStartTick(p, e.transport.Tick())
		p.chainArmed = true
	} else if position == p.chainLen {
		p.chainLen++
	}
	p.chain[position] = entry
}

func chainStartTick(p *patternTrack, tick int64) int64 {
	if p.active < 0 {
		return ((tick + seq.TicksPerStep - 1) / seq.TicksPerStep) * seq.TicksPerStep
	}
	start := p.startTick
	if tick <= start {
		return start
	}
	length := int64(p.slots[p.active].Len) * p.slots[p.active].GridTicks()
	return start + ((tick-start+length-1)/length)*length
}

func (e *Engine) advanceChains() {
	if !e.transport.Playing() || e.faulted {
		return
	}
	for track := 0; track < e.tracks && !e.faulted; track++ {
		p := &e.patterns[track]
		if !p.chainArmed || p.chainLen == 0 || e.transport.Tick() < p.chainDue {
			continue
		}
		entry := p.chain[p.chainNext]
		p.chainStart = e.transport.Tick()
		p.chainRepeat = entry.repeats
		e.selectPatternNow(track, int(entry.slot), true)
		p.chainNext = (p.chainNext + 1) % p.chainLen
		p.chainDue = e.transport.Tick() + int64(p.slots[entry.slot].Len)*p.slots[entry.slot].GridTicks()*int64(entry.repeats)
	}
}

func (e *Engine) chainTargetAt(track int, tick int64) (slot int, ok bool) {
	p := &e.patterns[track]
	if !p.chainArmed || p.chainLen == 0 || p.chainDue != tick {
		return 0, false
	}
	return int(p.chain[p.chainNext].slot), true
}

// restoreSourceChains reconstructs the authored phase after a song start or
// seek. Runtime chain commands keep their existing edit semantics.
//
//go:noinline
func (e *Engine) restoreSourceChains(tick int64) {
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		if p.sourceChainLen == 0 {
			continue
		}
		var period int64
		for i := uint8(0); i < p.sourceChainLen; i++ {
			slot := p.sourceChain[i]
			p.chain[i] = chainEntry{slot: slot, repeats: 1}
			period += int64(p.slots[slot].Len) * p.slots[slot].GridTicks()
		}
		position := tick % period
		for i := uint8(0); i < p.sourceChainLen; i++ {
			slot := p.sourceChain[i]
			length := int64(p.slots[slot].Len) * p.slots[slot].GridTicks()
			if position >= length {
				position -= length
				continue
			}
			e.selectPatternNow(track, int(slot), true)
			p.startTick = tick - position
			p.chainLen = p.sourceChainLen
			p.chainNext = (i + 1) % p.chainLen
			p.chainRepeat = 1
			p.chainStart = p.startTick
			p.chainDue = p.startTick + length
			p.chainArmed = true
			break
		}
	}
}
