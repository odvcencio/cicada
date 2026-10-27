package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
)

// automationLane names one parameter the automation timeline moves.
type automationLane struct {
	track uint8
	id    kernel.ParamID
}

// restartAutomationLanes starts a new pass of the timeline at the engine's own
// loop wrap. The host is not told about a wrap, so nothing may change except
// the parameters the timeline moves: mute, solo, positioned gain, sends,
// previews, committed edits and every parameter without a lane stay as they
// are, and nothing fades. Each lane's parameter alone returns, at once, to the
// value it has before the first point of a pass: its committed value if the
// saved parameter state has one, else its authored value. Points at or before
// the start of the pass replay after that. A parameter with both a lane and an
// active preview keeps being driven by its lane, as on the first pass.
func (e *Engine) restartAutomationLanes() bool {
	base := e.sceneDefaults
	if base == nil {
		return true
	}
	for _, lane := range e.automationLanes {
		value, ok := e.committedValue(lane.track, lane.id)
		if !ok {
			value, ok = e.authoredValue(lane, base)
		}
		if ok {
			e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: lane.track, Index: uint16(lane.id), Arg0: math.Float32bits(value)})
		} else if lane.id == kernel.ParamFxDelayTime && e.delayA != nil {
			e.setDelayDivision(base.delay.Division) // The authored time is a tempo division.
		} else if lane.id == kernel.ParamFxCompMakeup && e.compMusic != nil {
			params := e.compMusic.Params()
			params.MakeupAuto, params.MakeupDB = base.compressor.MakeupAuto, base.compressor.MakeupDB
			if e.compMusic.SetParams(params) != nil {
				e.fault(FaultParam)
			}
		}
		if e.faulted {
			return false
		}
	}
	return true
}

func (e *Engine) committedValue(track uint8, id kernel.ParamID) (float32, bool) {
	slot := int(track)
	if track == 0xff {
		slot = ParameterGlobalSlot
	}
	if slot > ParameterGlobalSlot || id >= kernel.ParamCount {
		return 0, false
	}
	bits := e.savedParameters[slot][id]
	return math.Float32frombits(uint32(bits)), bits>>32 != 0
}

// authoredValue reads a lane's parameter from the authored snapshot as the
// value a command carries. It reports false for what is not one number: a
// delay time set as a tempo division, and automatic comp makeup. Each group's
// registry IDs are consecutive, so one array stands for the group's switch.
func (e *Engine) authoredValue(lane automationLane, base *sceneParameterState) (float32, bool) {
	id := lane.id
	if lane.track == 0xff {
		d, r, c := &base.delay, &base.reverb, &base.compressor
		pingpong := 0.0
		if d.PingPong {
			pingpong = 1
		}
		switch {
		case id >= kernel.ParamFxDriveGain && id <= kernel.ParamFxDriveMix:
			for i := 0; i < e.tracks; i++ {
				if e.voices[i].insert != nil {
					g := &base.tracks[i].drive
					return float32([...]float64{g.GainDB, g.ToneHz, g.Mix}[id-kernel.ParamFxDriveGain]), true
				}
			}
		case id >= kernel.ParamFxDelayTime && id <= kernel.ParamFxDelayMix:
			return float32([...]float64{d.TimeMs, d.Feedback, d.DampHz, pingpong, d.Width, d.Mix}[id-kernel.ParamFxDelayTime]), id != kernel.ParamFxDelayTime || d.Division == fx.FreeDelay
		case id >= kernel.ParamFxReverbSize && id <= kernel.ParamFxReverbMix:
			return float32([...]float64{r.Size, r.DecaySec * 1000, r.DampHz, r.HighpassHz, r.PredelayMs, r.Mix}[id-kernel.ParamFxReverbSize]), true
		case id >= kernel.ParamFxCompThreshold && id <= kernel.ParamFxCompMix:
			return float32([...]float64{c.Threshold, c.Ratio, c.Knee, c.AttackMs, c.ReleaseMs, c.MakeupDB, c.Mix}[id-kernel.ParamFxCompThreshold]), id != kernel.ParamFxCompMakeup || !c.MakeupAuto
		}
		return 0, false
	}
	if int(lane.track) >= e.tracks {
		return 0, false
	}
	p := &base.tracks[lane.track]
	switch {
	case id >= kernel.ParamAmpDrive && id <= kernel.ParamAmpOutput:
		if !p.hasAmp {
			return 0, false
		}
		return float32(p.amp[id-kernel.ParamAmpDrive]), true
	case id == kernel.ParamMixGain && p.sourceOff:
		return float32(math.Inf(-1)), true
	case id >= kernel.ParamMixGain && id <= kernel.ParamMixSendB:
		return [...]float32{p.gainDB, p.pan, p.sendA, p.sendB}[id-kernel.ParamMixGain], true
	case id == kernel.ParamPianoSustain:
		return p.pianoSustain, true
	case id >= kernel.ParamAcidCutoff && id <= kernel.ParamAcidAccent:
		a := &p.acid
		return float32([...]float64{a.Cutoff, a.Resonance, a.EnvMod, a.Decay * 1000, a.Accent}[id-kernel.ParamAcidCutoff]), true
	case id >= kernel.ParamGuitarBend && id <= kernel.ParamGuitarDrive:
		return float32(p.guitar.Value(id)), true
	case isKeysControl(id):
		return p.keysValues[id-keysControlFirst], true
	}
	lane2, control, ok := drumParamControl(id)
	if !ok {
		return 0, false
	}
	d := &p.drums[lane2]
	if control == drumLevel && d.LevelDB <= -1000 {
		return float32(math.Inf(-1)), true
	}
	return float32([...]float64{d.Tune, d.Decay * 1000, d.LevelDB, d.Pan}[control]), true
}
