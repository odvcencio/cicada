package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// ParameterState stores registry values without allocating during reconstruction.
// Slot 16 holds global parameters; the high word marks present values.
type ParameterState [17][kernel.ParamCount]uint64

func (state *ParameterState) Set(track uint8, id kernel.ParamID, value float32) {
	if track == 0xff {
		track = 16
	}
	if track < 17 && id < kernel.ParamCount {
		state[track][id] = 1<<32 | uint64(math.Float32bits(value))
	}
}

func (state *ParameterState) Visit(visit func(SceneSetting, bool)) {
	state.visit(parameterVisitor{visit: visit})
}

// A value visitor avoids allocating method values or closures in TinyGo's
// render path. Native hosts may supply a callback to copy the same traversal.
type parameterVisitor struct {
	engine *Engine
	visit  func(SceneSetting, bool)
}

func (visitor parameterVisitor) apply(setting SceneSetting, snap bool) {
	if visitor.engine != nil {
		visitor.engine.applySceneSetting(setting, snap)
	} else {
		visitor.visit(setting, snap)
	}
}

func (state *ParameterState) visit(visitor parameterVisitor) {
	for track := range state {
		for id, bits := range state[track] {
			if bits>>32 == 0 {
				continue
			}
			index := uint8(track)
			if index == 16 {
				index = 0xff
			}
			visitor.apply(SceneSetting{Track: index, ID: kernel.ParamID(id), Value: math.Float32frombits(uint32(bits))}, true)
		}
	}
}

// ReconstructParameters visits saved defaults and the preceding/current song
// scenes in the same order used by seeking. Hosts call this on the render
// thread to rebuild cancellation values, using their complete saved defaults.
func (e *Engine) ReconstructParameters(defaults *ParameterState, tick int64, visit func(SceneSetting, bool)) {
	e.reconstructParameters(defaults, tick, parameterVisitor{visit: visit})
}

func (e *Engine) visitSceneParameters(index uint16, snap bool, visitor parameterVisitor) {
	if int(index) >= len(e.scenes) {
		return
	}
	for _, setting := range e.scenes[index].Settings {
		visitor.apply(setting, snap)
	}
}

// VisitSceneParameters visits the authored settings of a scene on the render thread.
func (e *Engine) VisitSceneParameters(index uint16, visit func(SceneSetting, bool)) {
	e.visitSceneParameters(index, false, parameterVisitor{visit: visit})
}

// VisitPendingSceneParameters visits same-tick scene launches before parameter
// restoration commands are queued. Future launches retain the current values.
func (e *Engine) VisitPendingSceneParameters(tick int64, visit func(SceneSetting, bool)) {
	for i := 0; i < e.pendingLen; i++ {
		e.visitPendingScene(e.pending[i], tick, visit)
	}
	for i := e.commandRead; i != e.commandWrite; i++ {
		e.visitPendingScene(e.commands[i%uint16(len(e.commands))], tick, visit)
	}
}

func (e *Engine) visitPendingScene(c cmd.Command, tick int64, visit func(SceneSetting, bool)) {
	if c.Op == cmd.OpLaunchScene && c.Arg0 == 0 && c.Tick <= tick {
		e.visitSceneParameters(c.Index, false, parameterVisitor{visit: visit})
	}
}

func (e *Engine) reconstructParameters(defaults *ParameterState, tick int64, visitor parameterVisitor) {
	defaults.visit(visitor)
	if e.placementSchedule || len(e.schedule) == 0 {
		return
	}
	index, cycleStart, tick := e.songPosition(tick)
	if index < 0 {
		return
	}
	for prior := 0; tick > cycleStart && prior < index; prior++ {
		e.visitSceneParameters(e.schedule[prior].Scene, true, visitor)
	}
	entry := e.schedule[index]
	if visitor.engine != nil {
		if tick == cycleStart+entry.Tick {
			e.settleSceneEffects()
		}
		e.resetClips()
		e.launchSceneTracks(entry.Scene, false)
	}
	e.visitSceneParameters(entry.Scene, tick > cycleStart+entry.Tick, visitor)
}

func (e *Engine) songPosition(tick int64) (index int, cycleStart, position int64) {
	if len(e.schedule) == 0 {
		return -1, 0, tick
	}
	total := e.schedule[len(e.schedule)-1].EndTick
	if tick >= total && !e.loopSong {
		tick = 0
	}
	if e.loopSong {
		cycleStart = tick / total * total
	}
	for index, entry := range e.schedule {
		if tick < cycleStart+entry.EndTick {
			return index, cycleStart, tick
		}
	}
	return -1, cycleStart, tick
}
