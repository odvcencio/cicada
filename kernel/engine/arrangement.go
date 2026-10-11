package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
)

type SceneMode uint8

const (
	SceneKeep SceneMode = iota
	SceneOff
	SceneSlot
	SceneClip
)

type SceneBinding struct {
	Mode SceneMode
	Slot uint8
	Clip uint16 `json:",omitempty"`
}

type SceneSetting struct {
	Track    uint8
	ID       kernel.ParamID
	Value    float32
	Division fx.DelayDivision
}

// Scene has one optional action per track. An omitted binding keeps the
// current slot; on the first scene that means the track stays off.
type Scene struct {
	Track    [16]SceneBinding
	Settings []SceneSetting
}

type SongEntry struct {
	Scene uint16
	Bars  uint16
}

func (e *Engine) applySceneCommand(c cmd.Command) {
	if int(c.Index) >= len(e.scenes) {
		e.fault(FaultSceneIndex)
		return
	}
	var at int64
	if c.Arg0 == 3 {
		var ok bool
		at, ok = e.scenePatternEndTick()
		if !ok {
			e.fault(FaultNoSharedPatternEnd)
			return
		}
	} else {
		var err error
		at, err = seq.QuantizeTick(e.transport.Tick(), cmd.Quantize(c.Arg0), 16)
		if err != nil {
			e.fault(FaultQuantize)
			return
		}
	}
	if at > e.transport.Tick() {
		if e.pendingLen == len(e.pending) {
			e.fault(FaultPendingFull)
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
	period, residue := int64(0), int64(0)
	active := false
	for track := 0; track < e.tracks; track++ {
		p := &e.patterns[track]
		if p.active < 0 {
			continue
		}
		length := int64(p.slots[p.active].Len) * p.slots[p.active].GridTicks()
		target := p.startTick % length
		if !active {
			period, residue, active = length, target, true
			continue
		}
		g := gcd(period, length)
		if (target-residue)%g != 0 {
			return 0, false
		}
		cycles := length / g
		if period > math.MaxInt64/cycles {
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
		period = seq.TicksPerBar
	}
	tick := e.transport.Tick()
	if tick > residue {
		delta := tick - residue
		cycles := delta / period
		if delta%period != 0 {
			cycles++
		}
		if cycles > (math.MaxInt64-residue)/period {
			return 0, false
		}
		residue += cycles * period
	}
	return residue, true
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (e *Engine) launchScene(index uint16) {
	e.launchSceneMode(index, false, false)
}

func (e *Engine) launchSceneWithSkip(index uint16, skipManualPatterns bool) {
	e.launchSceneMode(index, skipManualPatterns, false)
}

func (e *Engine) launchSceneMode(index uint16, skipManualPatterns, snapSettings bool) {
	e.launchSceneTracks(index, skipManualPatterns)
	e.applySceneSettingsMode(index, snapSettings)
}

func (e *Engine) launchSceneTracks(index uint16, skipManualPatterns bool) {
	e.sceneTick = e.transport.Tick()
	e.currentScene = int(index)
	e.sceneSequence++
	scene := &e.scenes[index]
	for track := 0; track < e.tracks && !e.faulted; track++ {
		if skipManualPatterns && e.manualPatternTick[track] == e.transport.Tick() {
			continue
		}
		binding := scene.Track[track]
		p := &e.patterns[track]
		switch binding.Mode {
		case SceneKeep:
		case SceneClip:
			e.startClip(track, binding.Clip, -1, 0)
		case SceneOff:
			e.stopClips(track)
			if e.fadeSceneTrack(track, true) {
				continue
			}
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
			if !e.nextPatternGeneration(track) {
				return
			}
			p.playingNote = 0
			p.heldValid = false
			p.eventCount, p.eventIndex = 0, 0
			e.emit(cmd.Message{Kind: cmd.Switched, Track: uint8(track), A: 0xffff, Tick: e.transport.Tick()})
		case SceneSlot:
			e.fadeSceneTrack(track, false)
			if p.active == int8(binding.Slot) {
				if v := &e.voices[track]; v.preparedClip && v.prepared.SelectSlot(binding.Slot, 0, e.transport.Playing()) != nil {
					e.fault(FaultPreparedVoice)
					return
				}
				p.chainArmed = false
				continue
			}
			e.applyPatternCommand(cmd.Command{Op: cmd.OpSelectPattern, Track: uint8(track), Index: uint16(binding.Slot)})
		}
	}
}

// CurrentSceneTick is the render-owned tick of the latest scene transition.
func (e *Engine) CurrentSceneTick() int64 { return e.sceneTick }

func (e *Engine) applySceneSettings(index uint16) {
	e.applySceneSettingsMode(index, false)
}

func (e *Engine) applySceneSettingsMode(index uint16, snap bool) {
	if int(index) >= len(e.scenes) {
		return
	}
	settings := e.scenes[index].Settings
	for i := range settings {
		e.applySceneSetting(settings[i], snap)
		if e.faulted {
			return
		}
	}
}

func (e *Engine) applySceneSetting(setting SceneSetting, snap bool) {
	if e.faulted {
		return
	}
	if setting.Division != fx.FreeDelay {
		e.setDelayDivision(setting.Division)
		if e.faulted {
			return
		}
		return
	}
	command := cmd.Command{Op: cmd.OpSetParam, Track: setting.Track, Index: uint16(setting.ID), Arg0: math.Float32bits(setting.Value)}
	if snap {
		e.setParamImmediate(command)
	} else {
		e.setParam(command)
	}
	if e.faulted {
		return
	}
}

func (e *Engine) startSong() {
	if e.placementSchedule {
		e.startPlacementSchedule()
		return
	}
	if len(e.schedule) == 0 {
		return
	}
	defer e.reapplyMacroLayers()
	if !e.restoreSceneDefaults() {
		return
	}
	index, cycleStart, tick := e.songPosition(e.transport.Tick())
	if index < 0 {
		return
	}
	if tick != e.transport.Tick() {
		_ = e.transport.SeekTick(tick)
	}
	e.restoreSourceChains(tick)
	entry := e.schedule[index]
	e.songMode, e.songIndex, e.songEndTick = true, index, cycleStart+entry.EndTick
	entryStart := cycleStart + entry.Tick
	// Reconstruction and host cancellation share the saved-default/scene walk.
	// At a boundary the current scene keeps its normal glide; earlier settings
	// and seeks into a scene settle immediately.
	e.reconstructParameters(&e.savedParameters, tick, parameterVisitor{engine: e})
	if e.faulted {
		return
	}
	if len(e.clipTemplates) > 0 {
		e.restoreSongClips(index, cycleStart, tick)
	}
	e.restorePreparedClips(index, cycleStart)
	if tick > entryStart {
		e.settleSceneEffects()
	}
}

func (e *Engine) advanceSong() {
	if e.placementSchedule {
		e.advancePlacementSchedule()
		return
	}
	if !e.songMode || !e.transport.Playing() || e.transport.Tick() < e.songEndTick {
		return
	}
	e.songIndex++
	if e.songIndex == len(e.schedule) {
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
	entry := e.schedule[e.songIndex]
	e.songEndTick += entry.EndTick - entry.Tick
	if e.manualSceneTick != e.transport.Tick() {
		e.launchSceneWithSkip(entry.Scene, true)
	}
}
