package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
)

type SceneMode uint8

const (
	SceneKeep SceneMode = iota
	SceneOff
	SceneSlot
)

type SceneBinding struct {
	Mode SceneMode
	Slot uint8
}

// Scene has one optional action per track. An omitted binding keeps the
// current slot; on the first scene that means the track stays off.
type Scene struct {
	Track [16]SceneBinding
}

type SongEntry struct {
	Scene uint16
	Bars  uint16
}

func (e *Engine) applySceneCommand(c cmd.Command) {
	if int(c.Index) >= len(e.scenes) {
		e.fault(17)
		return
	}
	length := uint8(16)
	if c.Arg0 == 3 {
		var ok bool
		length, ok = e.commonPatternLength()
		if !ok {
			e.fault(18)
			return
		}
	}
	at, err := seq.QuantizeTick(e.transport.Tick(), cmd.Quantize(c.Arg0), length)
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
	e.launchScene(c.Index)
}

func (e *Engine) commonPatternLength() (uint8, bool) {
	length := uint8(0)
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		if p.active < 0 {
			continue
		}
		if p.startStep != 0 {
			return 0, false
		}
		current := p.slots[p.active].Len
		if length != 0 && length != current {
			return 0, false
		}
		length = current
	}
	if length == 0 {
		length = 16
	}
	return length, true
}

func (e *Engine) launchScene(index uint16) {
	scene := &e.scenes[index]
	for track := 0; track < e.tracks && !e.faulted; track++ {
		binding := scene.Track[track]
		p := &e.patterns[track]
		switch binding.Mode {
		case SceneKeep:
		case SceneOff:
			if p.active < 0 {
				p.chainArmed = false
				continue
			}
			e.noteOff(track, 0xffff)
			if p.playingNote != 0 {
				e.emit(cmd.Message{Kind: cmd.NoteOff, Track: uint8(track), Tick: e.transport.Tick()})
			}
			p.active = -1
			p.chainArmed = false
			p.generation++
			p.playingNote = 0
			p.heldValid = false
			p.eventCount, p.eventIndex = 0, 0
			e.emit(cmd.Message{Kind: cmd.Switched, Track: uint8(track), A: 0xffff, Tick: e.transport.Tick()})
		case SceneSlot:
			if p.active == int8(binding.Slot) {
				p.chainArmed = false
				continue
			}
			e.applyPatternCommand(cmd.Command{Op: cmd.OpSelectPattern, Track: uint8(track), Index: uint16(binding.Slot)})
		}
	}
}

func (e *Engine) startSong() {
	if len(e.song) == 0 {
		return
	}
	var total int64
	for _, entry := range e.song {
		total += int64(entry.Bars) * seq.TicksPerBar
	}
	tick := e.transport.Tick()
	if tick >= total && !e.loopSong {
		_ = e.transport.SeekTick(0)
		tick = 0
	}
	cycleStart := int64(0)
	if e.loopSong {
		cycleStart = tick / total * total
	}
	end := cycleStart
	for i, entry := range e.song {
		end += int64(entry.Bars) * seq.TicksPerBar
		if tick < end {
			e.songMode = true
			e.songIndex = i
			e.songEndTick = end
			e.launchScene(entry.Scene)
			return
		}
	}
}

func (e *Engine) advanceSong() {
	if !e.songMode || !e.transport.Playing() || e.transport.Tick() < e.songEndTick {
		return
	}
	e.songIndex++
	if e.songIndex == len(e.song) {
		if !e.loopSong {
			e.songMode = false
			e.transport.Stop()
			for track := 0; track < e.tracks; track++ {
				e.noteOff(track, 0xffff)
				e.patterns[track].playingNote = 0
				e.patterns[track].heldValid = false
			}
			return
		}
		e.songIndex = 0
	}
	entry := e.song[e.songIndex]
	e.songEndTick += int64(entry.Bars) * seq.TicksPerBar
	e.launchScene(entry.Scene)
}
