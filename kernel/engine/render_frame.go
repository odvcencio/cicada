package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/spatial"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"math"
)

func (e *Engine) prepareRenderFrame(frame int, scheduledTempo *int64, outL, outR []float32) bool {
	e.renderFrame = frame
	if e.transport.BPMMilli() != (*scheduledTempo) {
		if e.delayA != nil && e.delayA.SetTempo(e.transport.BPMMilli()) != nil {
			e.fault(FaultDelayTempo)
			clear(outL[frame:])
			clear(outR[frame:])
			return false
		}
		e.scheduleAll()
		(*scheduledTempo) = e.transport.BPMMilli()
	}
	e.processPatternEvents(seq.NoteOff)
	e.advanceSong()
	if e.faulted {
		clear(outL[frame:])
		clear(outR[frame:])
		return false
	}
	e.applyPending()
	if e.faulted {
		clear(outL[frame:])
		clear(outR[frame:])
		return false
	}
	e.advanceAutomation()
	e.advanceDirector()
	e.processBarBoundary()
	if e.faulted {
		clear(outL[frame:])
		clear(outR[frame:])
		return false
	}
	e.processPatternEvents(seq.NoteOn)
	e.processPatternEvents(seq.NoteExpression)
	if e.faulted {
		clear(outL[frame:])
		clear(outR[frame:])
		return false
	}
	e.rawPlaying = e.transport.Playing()
	return true
}

func (e *Engine) nextRawTrack(track int) (left, right float32, fault uint16) {
	v := &e.voices[track]
	switch v.kind {
	case VoiceAcid:
		sample := v.acid.Next()
		if v.acid.Fault() {
			return 0, 0, FaultAcidVoice
		}
		left, right = sample, sample
	case VoiceDrums:
		left, right = v.drums.NextStereo()
		if v.drums.Fault() {
			return 0, 0, FaultDrumVoice
		}
	case VoiceGuitar:
		sample := v.guitar.Next()
		left, right = sample, sample
	case VoiceModal:
		sample := v.modal.Next()
		left, right = sample, sample
	case VoiceSample:
		left, right = v.sampler.NextStereo()
	case VoiceAudio:
		left, right = e.nextClipStereo(track)
	case VoiceGraph:
		var sample float32
		if v.poly != nil {
			sample = v.poly.Next()
		} else {
			sample = v.graph.Next()
		}
		left, right = sample, sample
	case VoicePiano:
		left, right = v.piano.NextStereo()
		left, right = left*keyboard.PianoLevelTrim, right*keyboard.PianoLevelTrim
	case VoiceKeys:
		if keysEnabled {
			left, right = v.keys.NextStereo()
		}
	case VoiceGraphPoly:
		left, right = v.legacyPoly.NextStereo()
	case VoicePrepared:
		left, right = v.prepared.NextStereo()
	}
	if v.chain != nil {
		left, right = v.chain.Process(left, right)
		if v.chain.fault {
			return 0, 0, FaultTrackInsert
		}
	}
	if v.insert != nil {
		left, right = v.insert.Process(left, right)
		if v.insert.Fault() {
			return 0, 0, FaultDriveInsert
		}
	}
	if v.align != nil {
		left, right = v.align.Process(left, right)
	}
	return left, right, 0
}

func (e *Engine) renderMixedFrame(frame int, outL, outR []float32) (float32, bool) {
	var dry mix.Dry
	var buses spatial.Frame
	var sendAL, sendAR, sendBL, sendBR, sideL, sideR float32
	for track := 0; track < e.tracks; track++ {
		v := &e.voices[track]
		positioned := &e.spatial.tracks[track]
		if positioned.enabled {
			positioned.gain += float32((positioned.target - positioned.gain) * v.mixSmooth)
		}
		v.mix.Left += float32((v.targetMix.Left - v.mix.Left) * v.mixSmooth)
		v.mix.Right += float32((v.targetMix.Right - v.mix.Right) * v.mixSmooth)
		v.sendA += float32((v.targetSendA - v.sendA) * v.sendSmooth)
		v.sendB += float32((v.targetSendB - v.sendB) * v.sendSmooth)
		if v.muteGain < v.muteTarget {
			v.muteGain = min(v.muteTarget, v.muteGain+v.muteStep)
		} else if v.muteGain > v.muteTarget {
			v.muteGain = max(v.muteTarget, v.muteGain-v.muteStep)
		}
		left, right, fault := e.renderedTrack(track, frame)
		if fault != 0 {
			e.fault(fault)
			clear(outL[frame:])
			clear(outR[frame:])
			return 0, false
		}
		left *= e.director[track].gain
		right *= e.director[track].gain
		var trackL, trackR float32
		if e.layerMask&(1<<track) != 0 && v.kind != VoiceOff {
			trackL, trackR = left*(v.mix.Left*v.muteGain), right*(v.mix.Right*v.muteGain)
			if v.sendA > 0 && v.muteGain > 0 && !v.sourceOff {
				if v.sendAPre {
					sendAL += float32(left * v.sendA * v.muteGain)
					sendAR += float32(right * v.sendA * v.muteGain)
				} else {
					sendAL += float32(left * v.mix.Left * v.muteGain * v.sendA)
					sendAR += float32(right * v.mix.Right * v.muteGain * v.sendA)
				}
			}
			if v.sendB > 0 && v.muteGain > 0 && !v.sourceOff {
				if v.sendBPre {
					sendBL += float32(left * v.sendB * v.muteGain)
					sendBR += float32(right * v.sendB * v.muteGain)
				} else {
					sendBL += float32(left * v.mix.Left * v.muteGain * v.sendB)
					sendBR += float32(right * v.mix.Right * v.muteGain * v.sendB)
				}
			}
			tap := mix.Track{Left: v.mix.Left * v.muteGain, Right: v.mix.Right * v.muteGain}
			if positioned.enabled {
				mono := float32((left+right)*.5) * float32(positioned.gain*v.muteGain)
				if v.sourceOff {
					mono = 0
				}
				trackL, trackR = mono, mono
				if v.busSFX {
					buses.SFX.Add(mono, positioned.coefficients)
				} else {
					buses.Music.Add(mono, positioned.coefficients)
				}
			} else if v.busSFX {
				dry.AddSFX(left, right, tap)
			} else {
				dry.Add(left, right, tap)
			}
			if e.compSidechainTrack == track+1 {
				sideL, sideR = trackL, trackR
			}
		}
		if e.taps != nil {
			e.taps[frame].Tracks[track] = Stereo{trackL, trackR}
		}
		accumulateMeter(&e.trackMeter[track], trackL, trackR)
	}
	if e.delayA != nil {
		returnL, returnR := e.delayA.Process(sendAL, sendAR)
		if e.delayA.Fault() {
			e.fault(FaultDelay)
			clear(outL[frame:])
			clear(outR[frame:])
			return 0, false
		}
		if e.taps != nil {
			e.taps[frame].ReturnA = Stereo{returnL, returnR}
		}
		accumulateMeter(&e.returnAMeter, returnL, returnR)
		dry.AddReturn(returnL, returnR)
	}
	if e.reverbB != nil {
		returnL, returnR := e.reverbB.Process(sendBL, sendBR)
		if e.reverbB.Fault() {
			e.fault(FaultReverb)
			clear(outL[frame:])
			clear(outR[frame:])
			return 0, false
		}
		if e.taps != nil {
			e.taps[frame].ReturnB = Stereo{returnL, returnR}
		}
		accumulateMeter(&e.returnBMeter, returnL, returnR)
		dry.AddReturn(returnL, returnR)
	}
	if e.spatial.active {
		buses.Music = e.spatial.rotation.Apply(buses.Music)
		buses.SFX = e.spatial.rotation.Apply(buses.SFX)
		spatialL, spatialR := e.spatial.music.Process(buses.Music)
		dry.AddReturn(spatialL, spatialR)
		spatialL, spatialR = e.spatial.sfx.Process(buses.SFX)
		dry.SFXLeft += spatialL
		dry.SFXRight += spatialR
	}
	if e.spatial.taps != nil {
		e.spatial.taps[frame] = buses
	}
	left, right := dry.Music()
	sfxL, sfxR := dry.SFX()
	if e.taps != nil {
		e.taps[frame].Music = Stereo{left, right}
		e.taps[frame].SFX = Stereo{sfxL, sfxR}
	}
	accumulateMeter(&e.sfxMeter, sfxL, sfxR)
	if e.sfxBusMute || e.musicBusSolo && !e.sfxBusSolo {
		sfxL, sfxR = 0, 0
	}
	if e.compMusic != nil {
		if e.compSidechainTrack == 0 {
			left, right = e.compMusic.Process(left, right)
		} else if e.compSidechainTrack == SFXSidechain {
			left, right = e.compMusic.ProcessSidechain(left, right, sfxL, sfxR)
		} else {
			left, right = e.compMusic.ProcessSidechain(left, right, sideL, sideR)
		}
		if e.compMusic.Fault() {
			e.fault(FaultCompressor)
			clear(outL[frame:])
			clear(outR[frame:])
			return 0, false
		}
		e.compGR = max(e.compGR, float32(e.compMusic.GainReductionDB()))
	}
	accumulateMeter(&e.musicMeter, left, right)
	if e.musicBusMute || e.sfxBusSolo && !e.musicBusSolo {
		left, right = 0, 0
	}
	left, right = left+sfxL, right+sfxR
	if e.masterMute {
		left, right = 0, 0
	}
	left += e.masterBiasL
	right += e.masterBiasR
	if e.masterGain != 1 {
		left *= e.masterGain
		right *= e.masterGain
	}
	if e.taps != nil {
		e.taps[frame].PreMaster = Stereo{left, right}
	}
	if e.masterProcessor != nil {
		left, right = e.masterProcessor.Process(left, right)
		if e.masterProcessor.Fault() {
			e.fault(FaultMasterProcessor)
			clear(outL[frame:])
			clear(outR[frame:])
			return 0, false
		}
		if e.taps != nil {
			if limiter, ok := e.masterProcessor.(interface{ LimiterGainReductionDB() float64 }); ok {
				e.taps[frame].MasterLimiterReductionDB = limiter.LimiterGainReductionDB()
			}
		}
	}
	accumulateMeter(&e.preMasterMeter, left, right)
	outL[frame], outR[frame], _ = e.limiter.Process(left, right)
	if e.limiter.Fault() {
		e.fault(FaultLimiter)
		clear(outL[frame:])
		clear(outR[frame:])
		return 0, false
	}
	if e.taps != nil {
		e.taps[frame].LimiterReductionDB = e.limiter.GainReductionDB()
	}
	e.limiterGR = max(e.limiterGR, float32(e.limiter.GainReductionDB()))
	accumulateMeter(&e.masterMeter, outL[frame], outR[frame])
	return float32(math.Max(math.Abs(float64(outL[frame])), math.Abs(float64(outR[frame])))), true
}

func (e *Engine) advanceRenderFrame() {
	e.advanceMacroRamps()
	e.transport.Advance(1)
	if e.transport.Playing() {
		e.playheadFrames--
		if e.playheadFrames <= 0 {
			e.emit(cmd.Message{Kind: cmd.Playhead, Track: 0xff, Tick: e.transport.Tick()})
			e.playheadFrames = e.sampleRate / 60
		}
	} else {
		e.playheadFrames = e.sampleRate / 60
	}
}

func (e *Engine) finishRender(frames int, blockPeak float32) {
	e.renderFrames = 0
	e.meterFrames += uint32(frames)
	e.meterBlock++
	if e.meterRate != 0 && e.meterBlock%e.meterRate == 0 {
		e.emitMeters(blockPeak)
	}
}
