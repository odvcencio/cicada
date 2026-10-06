package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

type directorTrack struct {
	gain, start, target float32
	frames, elapsed     uint32
	stop                bool
	stinger             bool
	endTick             int64
	slot                uint16
	fade                uint32
}

func (e *Engine) applyDirector(c cmd.Command) {
	e.liveEvents = true
	q := c.Arg0
	if c.Op == cmd.OpSetState {
		q >>= 16
	}
	tick := e.transport.Tick()
	at := tick
	if q == uint32(cmd.QuantizePhrase) {
		if e.phraseBars == 0 {
			e.fault(20)
			return
		}
		quantum := int64(e.phraseBars) * seq.TicksPerBar
		at = ((tick + quantum - 1) / quantum) * quantum
	} else {
		var err error
		at, err = seq.QuantizeTick(tick, cmd.Quantize(q), 16)
		if err != nil {
			e.fault(20)
			return
		}
	}
	if at > tick {
		if e.pendingLen == len(e.pending) {
			e.fault(5)
			return
		}
		c.Tick = at
		if c.Op == cmd.OpSetState {
			c.Arg0 &= 0xffff
		} else {
			c.Arg0 = 0
		}
		e.pending[e.pendingLen] = c
		e.pendingLen++
		return
	}
	if c.Op == cmd.OpSetState {
		scene := uint16(c.Arg0)
		if int(scene) >= len(e.scenes) {
			e.fault(17)
			return
		}
		e.songMode = false
		e.sceneFadeFrames = c.Arg1
		e.launchScene(scene)
		e.sceneFadeFrames = 0
		e.manualSceneTick = tick
		e.emit(cmd.Message{Kind: cmd.StateChanged, Track: 255, A: c.Index, B: uint32(scene), Tick: tick})
		return
	}
	track := int(c.Track)
	p := &e.patterns[track]
	d := &e.director[track]
	if e.voices[track].kind == VoiceOff || p.active >= 0 && !d.stinger {
		e.fault(21)
		return
	}
	if d.stinger {
		e.emit(cmd.Message{Kind: cmd.StingerEnded, Track: c.Track, A: d.slot, Tick: tick})
	}
	e.stopDirectorTrack(track)
	d.gain = 1
	if c.Arg1 > 0 {
		d.gain = 0
	}
	e.fadeDirectorTrack(track, 1, c.Arg1, false)
	d.stinger, d.slot, d.fade = true, c.Index, c.Arg1
	d.endTick = tick + int64(p.slots[c.Index].Len)*seq.TicksPerStep
	e.selectPatternNow(track, int(c.Index), true)
	p.chainArmed = false
	e.emit(cmd.Message{Kind: cmd.StingerStarted, Track: c.Track, A: c.Index, Tick: tick})
}

func (e *Engine) fadeDirectorTrack(track int, target float32, frames uint32, stop bool) {
	d := &e.director[track]
	d.start, d.target, d.frames, d.elapsed, d.stop = d.gain, target, frames, 0, stop
	if frames == 0 {
		d.gain = target
	}
}

// A shared track switches its pattern normally. Separate outgoing and incoming
// tracks keep their pattern clocks running throughout the linear crossfade.
func (e *Engine) fadeSceneTrack(track int, off bool) bool {
	d := &e.director[track]
	if d.stinger {
		return true
	}
	if off {
		if e.sceneFadeFrames != 0 && e.patterns[track].active >= 0 {
			e.fadeDirectorTrack(track, 0, e.sceneFadeFrames, true)
			return true
		}
		e.fadeDirectorTrack(track, d.gain, 0, false)
	} else {
		if e.sceneFadeFrames != 0 && e.patterns[track].active < 0 {
			d.gain = 0
		}
		e.fadeDirectorTrack(track, 1, e.sceneFadeFrames, false)
	}
	return false
}

func (e *Engine) stopDirectorTrack(track int) {
	p := &e.patterns[track]
	e.noteOff(track, 0xffff)
	p.active = -1
	p.chainArmed = false
	p.generation++
	p.playingNote = 0
	p.heldValid = false
	p.eventCount, p.eventIndex = 0, 0
}

func (e *Engine) advanceDirector() {
	for track := 0; track < e.tracks; track++ {
		d := &e.director[track]
		if d.stinger && e.transport.Playing() {
			remaining := e.transport.Clock().SampleAtTick(d.endTick) - e.transport.Sample()
			if remaining <= 0 {
				e.stopDirectorTrack(track)
				d.stinger = false
				d.frames, d.gain = 0, 0
				e.emit(cmd.Message{Kind: cmd.StingerEnded, Track: uint8(track), A: d.slot, Tick: d.endTick})
			} else if d.fade > 0 && uint64(remaining) <= uint64(d.fade) && d.target != 0 {
				e.fadeDirectorTrack(track, 0, uint32(remaining), false)
			}
		}
		if d.frames == 0 {
			continue
		}
		if d.elapsed >= d.frames {
			d.gain, d.frames = d.target, 0
			if d.stop {
				e.stopDirectorTrack(track)
				d.stop = false
			}
		} else {
			d.gain = d.start + (d.target-d.start)*(float32(d.elapsed)/float32(d.frames))
			d.elapsed++
		}
	}
}

func (e *Engine) resetDirector() {
	n := 0
	for i := 0; i < e.pendingLen; i++ {
		c := e.pending[i]
		if c.Op != cmd.OpSetState && c.Op != cmd.OpTriggerStinger {
			e.pending[n] = c
			n++
		}
	}
	e.pendingLen = n
	for track := 0; track < e.tracks; track++ {
		if e.director[track].stinger || e.director[track].stop {
			e.stopDirectorTrack(track)
		}
		e.director[track] = directorTrack{gain: 1}
	}
}
