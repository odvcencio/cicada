package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/voice/acid"
)

// automationLane names one parameter the automation timeline moves.
type automationLane struct {
	track uint8
	id    kernel.ParamID
}

// restartAutomationLanes starts a new pass of the timeline at the engine's own
// loop wrap. The host is not told about a wrap, so nothing else may change:
// mute, solo, positioned gain, sends, previews, committed edits and every
// parameter without a lane stay as they are. Only the parameters the
// timeline moves return to the value they hold before its first point: the
// committed value if the saved parameter state has one, else the authored
// value, applied at once. Points at or before the new pass's start then replay.
// A parameter that also has an active preview keeps being driven by its lane,
// as on the first pass.
func (e *Engine) restartAutomationLanes() bool {
	base := e.sceneDefaults
	if base == nil {
		return true
	}
	for _, lane := range e.automationLanes {
		if value, committed := e.committedValue(lane.track, lane.id); committed {
			e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: lane.track, Index: uint16(lane.id), Arg0: math.Float32bits(value)})
		} else if lane.track == 0xff {
			e.restoreFxField(lane.id, base)
		} else if int(lane.track) < e.tracks {
			e.restoreTrackField(lane.track, lane.id, &base.tracks[lane.track])
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

// restoreTrackField puts one automatable track parameter back to its authored
// value, copying the stored value exactly rather than converting it.
func (e *Engine) restoreTrackField(track uint8, id kernel.ParamID, p *sceneTrackParameters) {
	v := &e.voices[track]
	switch {
	case id == kernel.ParamMixGain || id == kernel.ParamMixPan:
		if id == kernel.ParamMixGain {
			value := p.gainDB
			if p.sourceOff {
				value = float32(math.Inf(-1))
			}
			e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value)})
		} else {
			e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(p.pan)})
		}
	case id == kernel.ParamMixSendA:
		v.sendA, v.targetSendA = p.sendA, p.sendA
	case id == kernel.ParamMixSendB:
		v.sendB, v.targetSendB = p.sendB, p.sendB
	case id == kernel.ParamPianoSustain:
		e.setParamImmediate(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(p.pianoSustain)})
	case isKeysControl(id):
		if keysEnabled && v.keysControl != nil && v.keysChanged&(1<<(id-keysControlFirst)) != 0 {
			entry := &keysControls[id-keysControlFirst]
			if v.keysControl.SetControl(int(entry.slot), p.keysValues[id-keysControlFirst], 1) != nil {
				e.fault(FaultParam)
				return
			}
			v.keysValues[id-keysControlFirst] = p.keysValues[id-keysControlFirst]
			v.keysChanged &^= 1 << (id - keysControlFirst)
		}
	case id == kernel.ParamGuitarBend || id == kernel.ParamGuitarVibrato || id == kernel.ParamGuitarBrightness || id == kernel.ParamGuitarDamping || id == kernel.ParamGuitarPickup || id == kernel.ParamGuitarDrive:
		if v.guitar != nil {
			params := v.guitar.Params()
			if params.Set(id, p.guitar.Value(id)) != nil || v.guitar.SetParams(params) != nil {
				e.fault(FaultParam)
			}
		}
	case id == kernel.ParamAcidCutoff || id == kernel.ParamAcidReso || id == kernel.ParamAcidEnvmod || id == kernel.ParamAcidDecay || id == kernel.ParamAcidAccent:
		if v.acid != nil {
			params := v.acidTarget
			copyAcidField(&params, &p.acid, id)
			v.acidTarget = params
			if v.acid.SetParams(params) != nil {
				e.fault(FaultParam)
			}
		}
	default:
		lane, control, ok := drumParamControl(id)
		if !ok || v.drums == nil {
			return
		}
		params := v.drumTargets[lane]
		switch control {
		case drumTune:
			params.Tune = p.drums[lane].Tune
		case drumDecay:
			params.Decay = p.drums[lane].Decay
		case drumLevel:
			params.LevelDB = p.drums[lane].LevelDB
		case drumPan:
			params.Pan = p.drums[lane].Pan
		}
		v.drumTargets[lane] = params
		if v.drums.SetParams(lane, params) != nil {
			e.fault(FaultParam)
		}
	}
}

func copyAcidField(dst, src *acid.Params, id kernel.ParamID) {
	switch id {
	case kernel.ParamAcidCutoff:
		dst.Cutoff = src.Cutoff
	case kernel.ParamAcidReso:
		dst.Resonance = src.Resonance
	case kernel.ParamAcidEnvmod:
		dst.EnvMod = src.EnvMod
	case kernel.ParamAcidDecay:
		dst.Decay = src.Decay
	case kernel.ParamAcidAccent:
		dst.Accent = src.Accent
	}
}

// restoreFxField puts one automatable effect parameter back to its authored
// value, leaving the effect's other parameters alone.
func (e *Engine) restoreFxField(id kernel.ParamID, base *sceneParameterState) {
	switch id {
	case kernel.ParamFxDriveGain, kernel.ParamFxDriveTone, kernel.ParamFxDriveMix:
		for i := 0; i < e.tracks; i++ {
			drive := e.voices[i].insert
			if drive == nil {
				continue
			}
			params, authored := drive.Params(), base.tracks[i].drive
			switch id {
			case kernel.ParamFxDriveGain:
				params.GainDB = authored.GainDB
			case kernel.ParamFxDriveTone:
				params.ToneHz = authored.ToneHz
			case kernel.ParamFxDriveMix:
				params.Mix = authored.Mix
			}
			if drive.SetParams(params) != nil {
				e.fault(FaultParam)
				return
			}
		}
	case kernel.ParamFxDelayTime, kernel.ParamFxDelayFeedback, kernel.ParamFxDelayDamp, kernel.ParamFxDelayPingpong, kernel.ParamFxDelayWidth, kernel.ParamFxDelayMix:
		if e.delayA == nil {
			return
		}
		params, authored := e.delayA.Params(), base.delay
		switch id {
		case kernel.ParamFxDelayTime:
			params.Division, params.TimeMs = authored.Division, authored.TimeMs
		case kernel.ParamFxDelayFeedback:
			params.Feedback = authored.Feedback
		case kernel.ParamFxDelayDamp:
			params.DampHz = authored.DampHz
		case kernel.ParamFxDelayPingpong:
			params.PingPong = authored.PingPong
		case kernel.ParamFxDelayWidth:
			params.Width = authored.Width
		case kernel.ParamFxDelayMix:
			params.Mix = authored.Mix
		}
		if e.delayA.SetParams(params) != nil {
			e.fault(FaultParam)
		}
	case kernel.ParamFxReverbSize, kernel.ParamFxReverbDecay, kernel.ParamFxReverbDamp, kernel.ParamFxReverbHighpass, kernel.ParamFxReverbPredelay, kernel.ParamFxReverbMix:
		if e.reverbB == nil {
			return
		}
		params, authored := e.reverbB.Params(), base.reverb
		switch id {
		case kernel.ParamFxReverbSize:
			params.Size = authored.Size
		case kernel.ParamFxReverbDecay:
			params.DecaySec = authored.DecaySec
		case kernel.ParamFxReverbDamp:
			params.DampHz = authored.DampHz
		case kernel.ParamFxReverbHighpass:
			params.HighpassHz = authored.HighpassHz
		case kernel.ParamFxReverbPredelay:
			params.PredelayMs = authored.PredelayMs
		case kernel.ParamFxReverbMix:
			params.Mix = authored.Mix
		}
		if e.reverbB.SetParams(params) != nil {
			e.fault(FaultParam)
		}
	case kernel.ParamFxCompThreshold, kernel.ParamFxCompRatio, kernel.ParamFxCompKnee, kernel.ParamFxCompAttack, kernel.ParamFxCompRelease, kernel.ParamFxCompMakeup, kernel.ParamFxCompMix:
		if e.compMusic == nil {
			return
		}
		params, authored := e.compMusic.Params(), base.compressor
		switch id {
		case kernel.ParamFxCompThreshold:
			params.Threshold = authored.Threshold
		case kernel.ParamFxCompRatio:
			params.Ratio = authored.Ratio
		case kernel.ParamFxCompKnee:
			params.Knee = authored.Knee
		case kernel.ParamFxCompAttack:
			params.AttackMs = authored.AttackMs
		case kernel.ParamFxCompRelease:
			params.ReleaseMs = authored.ReleaseMs
		case kernel.ParamFxCompMakeup:
			params.MakeupAuto, params.MakeupDB = authored.MakeupAuto, authored.MakeupDB
		case kernel.ParamFxCompMix:
			params.Mix = authored.Mix
		}
		if e.compMusic.SetParams(params) != nil {
			e.fault(FaultParam)
		}
	}
}
