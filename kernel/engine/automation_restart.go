package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

// automationLane names one parameter the automation timeline moves.
type automationLane struct {
	track uint8
	id    kernel.ParamID
}

// restartAutomationLanes starts a new pass of the timeline at the engine's own
// loop wrap. The host is not told about a wrap, so only what the timeline moves
// may change: mute, solo, positioned gain, sends, and every voice or effect
// without a lane stay as they are, and nothing fades. A lane's mixer value, or
// its voice's or effect's sound parameters, return to their authored values
// at once, then to their committed values from the saved parameter state;
// points at or before the start of the pass replay after that. Only an
// uncommitted preview on another parameter of the same voice or effect is lost.
// A parameter with both a lane and a preview keeps being driven by its lane,
// as on the first pass.
func (e *Engine) restartAutomationLanes() bool {
	base := e.sceneDefaults
	if base == nil {
		return true
	}
	for _, lane := range e.automationLanes {
		ok := true
		switch id := lane.id; {
		case lane.track == 0xff:
			ok = e.restoreEffects(base)
			for i := 0; i < e.tracks && ok; i++ {
				ok = e.voices[i].insert == nil || e.voices[i].insert.SetParams(base.tracks[i].drive) == nil
			}
		case int(lane.track) >= e.tracks:
		case id >= kernel.ParamMixGain && id <= kernel.ParamMixSendB:
			p := &base.tracks[lane.track]
			value := [...]float32{p.gainDB, p.pan, p.sendA, p.sendB}[id-kernel.ParamMixGain]
			if id == kernel.ParamMixGain && p.sourceOff {
				value = float32(math.Inf(-1))
			}
			e.applyLaneValue(lane.track, id, value)
		default:
			ok = e.restoreVoiceState(&e.voices[lane.track], &base.tracks[lane.track])
		}
		slot := int(lane.track)
		if lane.track == 0xff {
			slot = ParameterGlobalSlot
		}
		if ok && slot <= ParameterGlobalSlot {
			e.savedParameters.visitSlot(slot, parameterVisitor{engine: e})
		}
		if !ok || e.faulted {
			return false
		}
	}
	return true
}

func (e *Engine) applyLaneValue(track uint8, id kernel.ParamID, value float32) {
	e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value)})
}
