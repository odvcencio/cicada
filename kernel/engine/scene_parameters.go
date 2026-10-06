package engine

import (
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// Scene reconstruction starts from the authored parameter state, not the
// parameters left by the last playback position. DSP instances stay in place.
// This snapshot is allocated at construction, never on the audio thread.
type sceneParameterState struct {
	tracks     [16]sceneTrackParameters
	layerMask  uint32
	delay      fx.DelayParams
	reverb     fx.ReverbParams
	compressor fx.CompParams
}

type sceneTrackParameters struct {
	mix                       mix.Track
	gainDB, pan, sendA, sendB float32
	sourceOff                 bool
	pianoSustain              float32
	acid                      acid.Params
	drums                     [drum.LaneCount]drum.Params
	drive                     fx.DriveParams
}

func (e *Engine) captureSceneDefaults() {
	if len(e.song) == 0 {
		return
	}
	for _, scene := range e.scenes {
		if len(scene.Settings) != 0 {
			e.sceneDefaults = &sceneParameterState{layerMask: e.layerAuthored}
			break
		}
	}
	if e.sceneDefaults == nil {
		return
	}
	for i := 0; i < e.tracks; i++ {
		v := &e.voices[i]
		p := &e.sceneDefaults.tracks[i]
		p.mix, p.gainDB, p.pan = v.mix, v.gainDB, v.pan
		p.sendA, p.sendB, p.sourceOff = v.sendA, v.sendB, v.sourceOff
		p.acid, p.drums = v.acidTarget, v.drumTargets
		p.pianoSustain = v.pianoSustain
		if v.insert != nil {
			p.drive = v.insert.Params()
		}
	}
	if e.delayA != nil {
		e.sceneDefaults.delay = e.delayA.Params()
	}
	if e.reverbB != nil {
		e.sceneDefaults.reverb = e.reverbB.Params()
	}
	if e.compMusic != nil {
		e.sceneDefaults.compressor = e.compMusic.Params()
	}
}

func (e *Engine) restoreSceneDefaults() bool {
	base := e.sceneDefaults
	if base == nil {
		return true
	}
	e.layerAuthored, e.soloCount = base.layerMask, 0
	e.composeLayerMask()
	for i := 0; i < e.tracks; i++ {
		v, p := &e.voices[i], &base.tracks[i]
		v.mix, v.targetMix = p.mix, p.mix
		v.gainDB, v.pan, v.sourceOff = p.gainDB, p.pan, p.sourceOff
		v.sendA, v.targetSendA = p.sendA, p.sendA
		v.sendB, v.targetSendB = p.sendB, p.sendB
		v.muted, v.soloed = false, false
		v.muteGain, v.muteTarget = 1, 1
		v.acidTarget, v.drumTargets = p.acid, p.drums
		if v.piano != nil {
			v.pianoSustain = p.pianoSustain
			if v.piano.SetSustain(p.pianoSustain) != nil {
				e.fault(18)
				return false
			}
		}
		if v.acid != nil && v.acid.SetParams(p.acid) != nil {
			e.fault(18)
			return false
		}
		if v.drums != nil {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				if v.drums.SetParams(lane, p.drums[lane]) != nil {
					e.fault(18)
					return false
				}
			}
		}
		if v.insert != nil && v.insert.SetParams(p.drive) != nil {
			e.fault(18)
			return false
		}
	}
	if e.delayA != nil && e.delayA.SetParams(base.delay) != nil {
		e.fault(18)
		return false
	}
	if e.reverbB != nil && e.reverbB.SetParams(base.reverb) != nil {
		e.fault(18)
		return false
	}
	if e.compMusic != nil && e.compMusic.SetParams(base.compressor) != nil {
		e.fault(18)
		return false
	}
	return true
}

func (e *Engine) settleSceneEffects() {
	if e.sceneDefaults == nil {
		return
	}
	if e.delayA != nil {
		e.delayA.Reset()
	}
	if e.reverbB != nil {
		e.reverbB.Reset()
	}
	if e.compMusic != nil {
		e.compMusic.Reset()
	}
	for i := 0; i < e.tracks; i++ {
		if e.voices[i].insert != nil {
			e.voices[i].insert.Reset()
		}
	}
}
