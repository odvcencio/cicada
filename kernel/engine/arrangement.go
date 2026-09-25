package engine

import (
	"math"

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
	var at int64
	if c.Arg0 == 3 {
		var ok bool
		at, ok = e.scenePatternEndTick()
		if !ok {
			e.fault(18)
			return
		}
	} else {
		var err error
		at, err = seq.QuantizeTick(e.transport.Tick(), cmd.Quantize(c.Arg0), 16)
		if err != nil {
			e.fault(14)
			return
		}
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
	e.manualSceneTick = e.transport.Tick()
}

// scenePatternEndTick finds the first shared end of the active patterns,
// including restart offsets. Their lengths are at most 64 steps, so each
// congruence can be combined in bounded time without allocating.
func (e *Engine) scenePatternEndTick() (int64, bool) {
	const maxSteps = math.MaxInt64 / seq.TicksPerStep
	period, residue := int64(1), int64(0)
	active := false
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		if p.active < 0 {
			continue
		}
		active = true
		length := int64(p.slots[p.active].Len)
		target := p.startStep % length
		g := gcd(period, length)
		if (target-residue)%g != 0 {
			return 0, false
		}
		cycles := length / g
		if period > maxSteps/cycles {
			return 0, false
		}
		for k := int64(0); k < cycles; k++ {
			candidate := residue + period*k
			if candidate%length == target {
				residue = candidate
				break
			}
		}
		period *= cycles
	}
	if !active {
		period = 16
	}
	tick := e.transport.Tick()
	step := tick / seq.TicksPerStep
	if tick%seq.TicksPerStep != 0 {
		step++
	}
	if step > residue {
		delta := step - residue
		cycles := delta / period
		if delta%period != 0 {
			cycles++
		}
		if cycles > (maxSteps-residue)/period {
			return 0, false
		}
		residue += cycles * period
	}
	return residue * seq.TicksPerStep, true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
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
	if e.manualSceneTick != e.transport.Tick() {
		e.launchScene(entry.Scene)
	}
}
