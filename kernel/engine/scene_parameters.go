package engine

import (
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
)

// Scene reconstruction starts from the authored parameter state, not the
// parameters left by the last playback position. DSP instances stay in place.
// This snapshot is allocated at construction, never on the audio thread.
type sceneParameterState struct {
	tracks     [MaxTracks]sceneTrackParameters
	layerMask  trackMask
	soloCount  int
	delay      fx.DelayParams
	reverb     fx.ReverbParams
	compressor fx.CompParams
}

type sceneTrackParameters struct {
	mix                       mix.Track
	gainDB, pan, sendA, sendB float32
	sourceOff                 bool
	muted, soloed             bool
	muteGain, muteTarget      float32
	spatialGain, spatialTrack float32 // The positioned-track gain and its target.
	pianoSustain              float32
	keysValues                [keysControlCount]float32
	acid                      acid.Params
	drums                     [drum.LaneCount]drum.Params
	guitar                    guitar.Params
	drive                     fx.DriveParams
}

func (e *Engine) captureSceneDefaults() {
	if len(e.schedule) == 0 {
		return
	}
	// Scene settings and automation lanes both move parameters away from their
	// authored values, so either one needs the authored snapshot.
	if len(e.automation) != 0 {
		e.sceneDefaults = &sceneParameterState{layerMask: e.layerAuthored}
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
	e.sceneDefaults.soloCount = e.soloCount
	for i := 0; i < e.tracks; i++ {
		v := &e.voices[i]
		p := &e.sceneDefaults.tracks[i]
		p.mix, p.gainDB, p.pan = v.mix, v.gainDB, v.pan
		p.sendA, p.sendB, p.sourceOff = v.sendA, v.sendB, v.sourceOff
		p.muted, p.soloed, p.muteGain, p.muteTarget = v.muted, v.soloed, v.muteGain, v.muteTarget
		p.spatialGain, p.spatialTrack = e.spatial.tracks[i].gain, e.spatial.tracks[i].target
		p.acid, p.drums = v.acidTarget, v.drumTargets
		p.pianoSustain = v.pianoSustain
		p.keysValues = v.keysValues
		if v.guitar != nil {
			p.guitar = v.guitar.Params()
		}
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

// restoreAuthoredAndCommitted returns every parameter to the state a fresh start
// has before the scene and the automation run: the authored defaults, then the
// committed edits in the saved parameter state (the values seek reconstruction
// replays). Loop wraps and arrange starts use it, so a restart never drops a
// committed edit.
func (e *Engine) restoreAuthoredAndCommitted() bool {
	if !e.restoreSceneDefaults() {
		return false
	}
	e.savedParameters.visit(parameterVisitor{engine: e})
	return !e.faulted
}

func (e *Engine) restoreSceneDefaults() bool {
	base := e.sceneDefaults
	if base == nil {
		return true
	}
	e.setSceneTempo(e.bpmMilli)
	e.liveTempo = false
	e.layerAuthored, e.soloCount = base.layerMask, base.soloCount
	e.composeLayerMask()
	for i := 0; i < e.tracks; i++ {
		e.restoreMixerState(i, &base.tracks[i])
		if !e.restoreVoiceState(&e.voices[i], &base.tracks[i]) {
			return false
		}
	}
	return e.restoreEffects(base)
}

// restoreEffects returns the delay, reverb and compressor to their authored
// parameters.
func (e *Engine) restoreEffects(base *sceneParameterState) bool {
	failed := e.delayA != nil && e.delayA.SetParams(base.delay) != nil ||
		e.reverbB != nil && e.reverbB.SetParams(base.reverb) != nil || e.compMusic != nil && e.compMusic.SetParams(base.compressor) != nil
	if failed {
		e.fault(FaultParam)
	}
	return !failed
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

// restoreMixerState puts one track's level, sends, mute, solo and positioned
// gain back to their authored values.
func (e *Engine) restoreMixerState(i int, p *sceneTrackParameters) {
	v := &e.voices[i]
	v.mix, v.targetMix = p.mix, p.mix
	v.gainDB, v.pan, v.sourceOff = p.gainDB, p.pan, p.sourceOff
	v.sendA, v.targetSendA = p.sendA, p.sendA
	v.sendB, v.targetSendB = p.sendB, p.sendB
	v.muted, v.soloed = p.muted, p.soloed
	v.muteGain, v.muteTarget = p.muteGain, p.muteTarget
	e.spatial.tracks[i].gain, e.spatial.tracks[i].target = p.spatialGain, p.spatialTrack
}

// restoreVoiceState puts one voice's sound parameters back to their authored
// values: sustain, keyboard controls, guitar, acid, drum lanes and its insert.
func (e *Engine) restoreVoiceState(v *voiceSlot, p *sceneTrackParameters) bool {
	v.acidTarget, v.drumTargets = p.acid, p.drums
	failed := false
	if v.piano != nil {
		v.pianoSustain = p.pianoSustain
		failed = v.piano.SetSustain(p.pianoSustain) != nil
	}
	if keysEnabled && v.keys != nil {
		v.pianoSustain = p.pianoSustain
		failed = failed || v.keys.SetSustain(p.pianoSustain) != nil || v.keysControl != nil && !e.restoreKeysControls(v, &p.keysValues)
	}
	failed = failed || v.guitar != nil && v.guitar.SetParams(p.guitar) != nil || v.acid != nil && v.acid.SetParams(p.acid) != nil ||
		v.insert != nil && v.insert.SetParams(p.drive) != nil
	if v.drums != nil {
		for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
			failed = failed || v.drums.SetParams(lane, p.drums[lane]) != nil
		}
	}
	if failed {
		e.fault(FaultParam)
	}
	return !failed
}
