package render

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/project"
)

// Keep the legacy offline scheduler, but use playback's compiled scene values,
// source ordering, persistent targets, and sample-by-sample parameter glides.
type sceneParameters struct {
	automation      []offlineControl
	automationIndex int
	scenes          map[string][]engine.SceneSetting
	tracks          []trackRuntime
	delay           *fx.Delay
	reverb          *fx.Reverb
	compressor      *fx.Compressor
	sampleRate      int
}

type sceneTrackParameters struct {
	mix, targetMix                                      mix.Track
	gainDB, pan, sendA, sendB, targetSendA, targetSendB float32
	mixAlpha, sendAlpha, muteGain, muteTarget, muteStep float32
	muted, solo, sourceOff                              bool
	acid                                                acid.Params
	drums                                               [drum.LaneCount]drum.Params
}

func hasSceneParameters(p *project.Project) bool {
	if len(p.Automation) > 0 {
		return true
	}
	for _, scene := range p.Scenes {
		if len(scene.Settings) != 0 {
			return true
		}
	}
	return false
}

func sceneSmoothingAlpha(id kernel.ParamID, sampleRate int) float32 {
	return float32(1 - math.Exp(-1/(float64(kernel.Params[id].SmoothingMS)*.001*float64(sampleRate))))
}

func compileSceneParameters(p *project.Project, tracks []trackRuntime, sampleRate int, delay *fx.Delay, reverb *fx.Reverb, compressor *fx.Compressor) (*sceneParameters, error) {
	if !hasSceneParameters(p) {
		return nil, nil
	}
	parameters := &sceneParameters{scenes: map[string][]engine.SceneSetting{}, tracks: tracks, delay: delay, reverb: reverb, compressor: compressor, sampleRate: sampleRate}
	for _, scene := range p.Scenes {
		settings, err := project.CompileSceneSettings(p, scene)
		if err != nil {
			return nil, err
		}
		parameters.scenes[scene.ID] = settings
	}
	for i := range tracks {
		track := &tracks[i]
		mixer := p.Tracks[i].Mixer
		state := &sceneTrackParameters{
			mix:    mix.NewTrack(mixer.GainDB, mixer.Pan, mixer.Mute),
			gainDB: float32(mixer.GainDB), pan: float32(mixer.Pan),
			sendA: track.sendA, sendB: track.sendB, targetSendA: track.sendA, targetSendB: track.sendB,
			mixAlpha: sceneSmoothingAlpha(kernel.ParamMixGain, sampleRate), sendAlpha: sceneSmoothingAlpha(kernel.ParamMixSendA, sampleRate),
			muteGain: 1, muteStep: 1 / float32(math.Ceil(float64(kernel.Params[kernel.ParamMixMute].SmoothingMS)*.001*float64(sampleRate))),
			muted: mixer.Mute, solo: mixer.Solo, sourceOff: mixer.Mute,
		}
		state.targetMix = state.mix
		if voice, ok := track.voice.(acidVoice); ok {
			state.acid = voice.Params()
		}
		if track.drums != nil {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				state.drums[lane] = track.drums.Params(lane)
			}
		}
		track.parameters = state
	}
	controls, err := project.CompileAutomation(p)
	if err != nil {
		return nil, err
	}
	clock, err := seq.NewClock(sampleRate, int64(p.TempoMilli))
	if err != nil {
		return nil, err
	}
	for _, control := range controls {
		parameters.automation = append(parameters.automation, offlineControl{Sample: clock.SampleAtTick(control.Tick), Setting: engine.SceneSetting{Track: control.Track, ID: kernel.ParamID(control.Index), Value: math.Float32frombits(control.Arg0)}})
	}
	parameters.updateMuteTargets()
	return parameters, nil
}

func (p *sceneParameters) updateMuteTargets() {
	anySolo := false
	for i := range p.tracks {
		anySolo = anySolo || p.tracks[i].parameters.solo
	}
	for i := range p.tracks {
		state := p.tracks[i].parameters
		state.muteTarget = 1
		if state.muted || anySolo && !state.solo {
			state.muteTarget = 0
		}
	}
}

func (p *sceneTrackParameters) advance(track *trackRuntime) {
	p.mix.Left += (p.targetMix.Left - p.mix.Left) * p.mixAlpha
	p.mix.Right += (p.targetMix.Right - p.mix.Right) * p.mixAlpha
	p.sendA += (p.targetSendA - p.sendA) * p.sendAlpha
	p.sendB += (p.targetSendB - p.sendB) * p.sendAlpha
	if p.muteGain < p.muteTarget {
		p.muteGain = min(p.muteTarget, p.muteGain+p.muteStep)
	} else if p.muteGain > p.muteTarget {
		p.muteGain = max(p.muteTarget, p.muteGain-p.muteStep)
	}
	track.mixer = mix.Track{Left: p.mix.Left * p.muteGain, Right: p.mix.Right * p.muteGain}
	if p.sourceOff {
		track.mixer = mix.Track{}
	}
	track.sendA, track.sendB = p.sendA, p.sendB
	track.sendPreGain = p.muteGain
	track.muted = p.sourceOff || p.muteGain == 0
}

func (p *sceneParameters) apply(scene string) error {
	if p == nil {
		return nil
	}
	for _, setting := range p.scenes[scene] {
		if err := p.set(setting); err != nil {
			return fmt.Errorf("scene %s parameter %d: %w", scene, setting.ID, err)
		}
	}
	return nil
}

func (p *sceneParameters) set(setting engine.SceneSetting) error {
	if setting.Division != fx.FreeDelay {
		if p.delay == nil {
			return fmt.Errorf("delay return is missing")
		}
		params := p.delay.Params()
		params.Division, params.TimeMs = setting.Division, 0
		return p.delay.SetParams(params)
	}
	spec, ok := kernel.Param(setting.ID)
	if !ok || !spec.Live {
		return fmt.Errorf("parameter is not live")
	}
	value := setting.Value
	if spec.Scope != "track" {
		return p.setGlobal(setting.ID, value)
	}
	if int(setting.Track) >= len(p.tracks) {
		return fmt.Errorf("track is missing")
	}
	track := &p.tracks[setting.Track]
	state := track.parameters
	off := spec.Off && math.IsInf(float64(value), -1)
	switch setting.ID {
	case kernel.ParamMixGain:
		state.gainDB, state.sourceOff = value, off
		if off {
			state.targetMix = mix.Track{}
		} else {
			state.targetMix = mix.NewTrack(float64(state.gainDB), float64(state.pan), false)
		}
	case kernel.ParamMixPan:
		state.pan = value
		if state.sourceOff {
			state.targetMix = mix.Track{}
		} else {
			state.targetMix = mix.NewTrack(float64(state.gainDB), float64(state.pan), false)
		}
	case kernel.ParamMixSendA:
		state.targetSendA = value
	case kernel.ParamMixSendB:
		state.targetSendB = value
	case kernel.ParamMixMute:
		state.muted = value == 1
		p.updateMuteTargets()
	case kernel.ParamMixSolo:
		state.solo = value == 1
		p.updateMuteTargets()
	case kernel.ParamPianoSustain:
		voice, ok := track.voice.(*pianoVoice)
		if !ok {
			return fmt.Errorf("piano voice is missing")
		}
		return voice.SetSustain(value)
	case kernel.ParamGuitarBend, kernel.ParamGuitarVibrato, kernel.ParamGuitarBrightness, kernel.ParamGuitarDamping, kernel.ParamGuitarPickup, kernel.ParamGuitarDrive:
		voice, ok := track.voice.(*guitar.Voice)
		if !ok {
			return fmt.Errorf("guitar voice is missing")
		}
		return voice.SetParam(setting.ID, float64(value))
	case kernel.ParamAcidCutoff, kernel.ParamAcidReso, kernel.ParamAcidEnvmod, kernel.ParamAcidDecay, kernel.ParamAcidAccent:
		voice, ok := track.voice.(acidVoice)
		if !ok {
			return fmt.Errorf("acid voice is missing")
		}
		params := state.acid
		switch setting.ID {
		case kernel.ParamAcidCutoff:
			params.Cutoff = float64(value)
		case kernel.ParamAcidReso:
			params.Resonance = float64(value)
		case kernel.ParamAcidEnvmod:
			params.EnvMod = float64(value)
		case kernel.ParamAcidDecay:
			params.Decay = float64(value) / 1000
		case kernel.ParamAcidAccent:
			params.Accent = float64(value)
		}
		state.acid = params
		return voice.SetParamsTarget(params, float64(sceneSmoothingAlpha(setting.ID, p.sampleRate)))
	default:
		lane, control, ok := sceneDrumControl(setting.ID)
		if !ok || track.drums == nil {
			return fmt.Errorf("drum parameter has no lane")
		}
		params := state.drums[lane]
		switch control {
		case "tune":
			params.Tune = float64(value)
		case "decay":
			params.Decay = float64(value) / 1000
		case "level":
			params.LevelDB = float64(value)
			if off {
				params.LevelDB = -1000
			}
		case "pan":
			params.Pan = float64(value)
		}
		state.drums[lane] = params
		return track.drums.SetParamsTarget(lane, params, float64(sceneSmoothingAlpha(setting.ID, p.sampleRate)))
	}
	return nil
}

func sceneDrumControl(id kernel.ParamID) (drum.Lane, string, bool) {
	switch id {
	case kernel.ParamDrumBdTune:
		return drum.BD, "tune", true
	case kernel.ParamDrumBdDecay:
		return drum.BD, "decay", true
	case kernel.ParamDrumBdLevel:
		return drum.BD, "level", true
	case kernel.ParamDrumBdPan:
		return drum.BD, "pan", true
	case kernel.ParamDrumSdTune:
		return drum.SD, "tune", true
	case kernel.ParamDrumSdDecay:
		return drum.SD, "decay", true
	case kernel.ParamDrumSdLevel:
		return drum.SD, "level", true
	case kernel.ParamDrumSdPan:
		return drum.SD, "pan", true
	case kernel.ParamDrumChTune:
		return drum.CH, "tune", true
	case kernel.ParamDrumChDecay:
		return drum.CH, "decay", true
	case kernel.ParamDrumChLevel:
		return drum.CH, "level", true
	case kernel.ParamDrumChPan:
		return drum.CH, "pan", true
	case kernel.ParamDrumOhTune:
		return drum.OH, "tune", true
	case kernel.ParamDrumOhDecay:
		return drum.OH, "decay", true
	case kernel.ParamDrumOhLevel:
		return drum.OH, "level", true
	case kernel.ParamDrumOhPan:
		return drum.OH, "pan", true
	case kernel.ParamDrumCpTune:
		return drum.CP, "tune", true
	case kernel.ParamDrumCpDecay:
		return drum.CP, "decay", true
	case kernel.ParamDrumCpLevel:
		return drum.CP, "level", true
	case kernel.ParamDrumCpPan:
		return drum.CP, "pan", true
	case kernel.ParamDrumRsTune:
		return drum.RS, "tune", true
	case kernel.ParamDrumRsDecay:
		return drum.RS, "decay", true
	case kernel.ParamDrumRsLevel:
		return drum.RS, "level", true
	case kernel.ParamDrumRsPan:
		return drum.RS, "pan", true
	case kernel.ParamDrumLtTune:
		return drum.LT, "tune", true
	case kernel.ParamDrumLtDecay:
		return drum.LT, "decay", true
	case kernel.ParamDrumLtLevel:
		return drum.LT, "level", true
	case kernel.ParamDrumLtPan:
		return drum.LT, "pan", true
	case kernel.ParamDrumMtTune:
		return drum.MT, "tune", true
	case kernel.ParamDrumMtDecay:
		return drum.MT, "decay", true
	case kernel.ParamDrumMtLevel:
		return drum.MT, "level", true
	case kernel.ParamDrumMtPan:
		return drum.MT, "pan", true
	case kernel.ParamDrumHtTune:
		return drum.HT, "tune", true
	case kernel.ParamDrumHtDecay:
		return drum.HT, "decay", true
	case kernel.ParamDrumHtLevel:
		return drum.HT, "level", true
	case kernel.ParamDrumHtPan:
		return drum.HT, "pan", true
	case kernel.ParamDrumCbTune:
		return drum.CB, "tune", true
	case kernel.ParamDrumCbDecay:
		return drum.CB, "decay", true
	case kernel.ParamDrumCbLevel:
		return drum.CB, "level", true
	case kernel.ParamDrumCbPan:
		return drum.CB, "pan", true
	case kernel.ParamDrumCyTune:
		return drum.CY, "tune", true
	case kernel.ParamDrumCyDecay:
		return drum.CY, "decay", true
	case kernel.ParamDrumCyLevel:
		return drum.CY, "level", true
	case kernel.ParamDrumCyPan:
		return drum.CY, "pan", true
	default:
		return 0, "", false
	}
}

func (p *sceneParameters) setGlobal(id kernel.ParamID, value float32) error {
	switch id {
	case kernel.ParamFxDriveGain, kernel.ParamFxDriveTone, kernel.ParamFxDriveMix:
		for track := 0; track < len(p.tracks); track++ {
			drive := p.tracks[track].insert
			if drive == nil {
				continue
			}
			params := drive.Params()
			switch id {
			case kernel.ParamFxDriveGain:
				params.GainDB = float64(value)
			case kernel.ParamFxDriveTone:
				params.ToneHz = float64(value)
			case kernel.ParamFxDriveMix:
				params.Mix = float64(value)
			}
			if err := drive.SetParams(params); err != nil {
				return err
			}
		}
	case kernel.ParamFxDelayTime, kernel.ParamFxDelayFeedback, kernel.ParamFxDelayDamp, kernel.ParamFxDelayPingpong, kernel.ParamFxDelayWidth, kernel.ParamFxDelayMix:
		if p.delay != nil {
			params := p.delay.Params()
			switch id {
			case kernel.ParamFxDelayTime:
				params.Division, params.TimeMs = fx.FreeDelay, float64(value)
			case kernel.ParamFxDelayFeedback:
				params.Feedback = float64(value)
			case kernel.ParamFxDelayDamp:
				params.DampHz = float64(value)
			case kernel.ParamFxDelayPingpong:
				params.PingPong = value == 1
			case kernel.ParamFxDelayWidth:
				params.Width = float64(value)
			case kernel.ParamFxDelayMix:
				params.Mix = float64(value)
			}
			if err := p.delay.SetParams(params); err != nil {
				return err
			}
		}
	case kernel.ParamFxReverbSize, kernel.ParamFxReverbDecay, kernel.ParamFxReverbDamp, kernel.ParamFxReverbHighpass, kernel.ParamFxReverbPredelay, kernel.ParamFxReverbMix:
		if p.reverb != nil {
			params := p.reverb.Params()
			switch id {
			case kernel.ParamFxReverbSize:
				params.Size = float64(value)
			case kernel.ParamFxReverbDecay:
				params.DecaySec = float64(value) / 1000
			case kernel.ParamFxReverbDamp:
				params.DampHz = float64(value)
			case kernel.ParamFxReverbHighpass:
				params.HighpassHz = float64(value)
			case kernel.ParamFxReverbPredelay:
				params.PredelayMs = float64(value)
			case kernel.ParamFxReverbMix:
				params.Mix = float64(value)
			}
			if err := p.reverb.SetParams(params); err != nil {
				return err
			}
		}
	case kernel.ParamFxCompThreshold, kernel.ParamFxCompRatio, kernel.ParamFxCompKnee, kernel.ParamFxCompAttack, kernel.ParamFxCompRelease, kernel.ParamFxCompMakeup, kernel.ParamFxCompMix:
		if p.compressor != nil {
			params := p.compressor.Params()
			switch id {
			case kernel.ParamFxCompThreshold:
				params.Threshold = float64(value)
			case kernel.ParamFxCompRatio:
				params.Ratio = float64(value)
			case kernel.ParamFxCompKnee:
				params.Knee = float64(value)
			case kernel.ParamFxCompAttack:
				params.AttackMs = float64(value)
			case kernel.ParamFxCompRelease:
				params.ReleaseMs = float64(value)
			case kernel.ParamFxCompMakeup:
				params.MakeupAuto, params.MakeupDB = false, float64(value)
			case kernel.ParamFxCompMix:
				params.Mix = float64(value)
			}
			if err := p.compressor.SetParams(params); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported effect parameter")
	}

	return nil
}

type offlineControl struct {
	Sample  int64
	Setting engine.SceneSetting
}

func (p *sceneParameters) advanceAutomation(sample int64) error {
	if p == nil {
		return nil
	}
	for p.automationIndex < len(p.automation) && p.automation[p.automationIndex].Sample <= sample {
		if err := p.set(p.automation[p.automationIndex].Setting); err != nil {
			return err
		}
		p.automationIndex++
	}
	return nil
}
