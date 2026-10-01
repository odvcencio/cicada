package engine

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/sample"
	"math"
)

// AudioAsset is immutable decoded planar PCM, prepared by a host worker.
type AudioAsset struct {
	Left, Right []float32
	SampleRate  int
}

type SamplerConfig struct {
	Asset   uint16
	RootKey uint8
	Voices  uint8
	Loop    bool
}
type ClipConfig struct {
	Asset                                             uint16
	StartFrame, EndFrame, FadeInFrames, FadeOutFrames int64
	GainDB                                            float64
}
type clipPlayback struct {
	voice              sample.Voice
	track              int
	clip               uint16
	id                 int
	sourceFrame, ratio float64
	active             bool
}

func (e *Engine) prepareClips(cfg *Config) error {
	if len(cfg.Clips) > 65535 || len(cfg.Assets) > 65535 {
		return Error("clip table exceeds index range")
	}
	e.clipTemplates = make([]sample.Voice, len(cfg.Clips))
	e.clipSpecs = append([]ClipConfig(nil), cfg.Clips...)
	for i, c := range cfg.Clips {
		if int(c.Asset) >= len(cfg.Assets) || c.StartFrame < 0 || c.EndFrame <= c.StartFrame || c.EndFrame > 1<<30 || c.FadeInFrames < 0 || c.FadeOutFrames < 0 || c.FadeInFrames > c.EndFrame-c.StartFrame || c.FadeOutFrames > c.EndFrame-c.StartFrame || math.IsNaN(c.GainDB) || math.IsInf(c.GainDB, 0) || c.GainDB < -60 || c.GainDB > 24 {
			return Error("clip region is invalid")
		}
		a := cfg.Assets[c.Asset]
		region := sample.Region{Left: a.Left, Right: a.Right, SampleRate: a.SampleRate, RootKey: 60, Start: int(c.StartFrame), End: int(c.EndFrame)}
		voice, err := sample.New(cfg.SampleRate, region)
		if err != nil {
			return err
		}
		if err = voice.SetParams(sample.Params{Gain: math.Pow(10, c.GainDB/20)}); err != nil {
			return err
		}
		e.clipTemplates[i] = *voice
	}
	return nil
}
func (e *Engine) startClip(track int, clip uint16, id int, elapsed int64) {
	if int(clip) >= len(e.clipTemplates) {
		e.fault(17)
		return
	}
	for i := range e.clipVoices[:e.clipLimit] {
		v := &e.clipVoices[i]
		if v.active {
			continue
		}
		*v = clipPlayback{voice: e.clipTemplates[clip], track: track, clip: clip, id: id, active: true}
		if v.voice.NoteOn(60, 127) != nil {
			e.fault(17)
			return
		}
		v.ratio = v.voice.Ratio()
		v.sourceFrame = float64(elapsed) * v.ratio
		v.voice.SeekFrames(elapsed)
		if !v.voice.Active() {
			v.active = false
		}
		return
	}
	e.fault(17)
}
func (e *Engine) stopClips(track int) {
	for i := range e.clipVoices {
		v := &e.clipVoices[i]
		if track < 0 || v.track == track {
			v.voice.Reset()
			v.active = false
		}
	}
}
func (e *Engine) resetClips() { e.clipVoices = [32]clipPlayback{} }
func (e *Engine) nextClipStereo(track int) (float32, float32) {
	if !e.transport.Playing() {
		return 0, 0
	}
	var left, right float64
	for i := range e.clipVoices {
		v := &e.clipVoices[i]
		if !v.active || v.track != track {
			continue
		}
		c := e.clipSpecs[v.clip]
		gain := float64(1)
		if c.FadeInFrames > 0 {
			gain = min(gain, v.sourceFrame/float64(c.FadeInFrames))
		}
		if c.FadeOutFrames > 0 {
			gain = min(gain, (float64(c.EndFrame-c.StartFrame)-v.sourceFrame)/float64(c.FadeOutFrames))
		}
		gain = max(0, gain)
		l, r := v.voice.NextStereo()
		left += float64(l) * gain
		right += float64(r) * gain
		v.sourceFrame += v.ratio
		v.active = v.voice.Active() && v.sourceFrame < float64(c.EndFrame-c.StartFrame)
	}
	return float32(left), float32(right)
}

func (e *Engine) restoreSongClips(index int, cycleStart, tick int64) {
	e.resetClips()
	var selected [16]int
	for t := range selected {
		selected[t] = -1
	}
	for i := 0; i <= index; i++ {
		event := e.schedule[i]
		for t, b := range e.scenes[event.Scene].Track {
			if t >= e.tracks || e.voices[t].kind != VoiceAudio {
				continue
			}
			if b.Mode == SceneClip {
				selected[t] = i
			} else if b.Mode == SceneOff {
				selected[t] = -1
			}
		}
	}
	clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.transport.BPMMilli()}
	for track, index := range selected {
		if index < 0 {
			continue
		}
		v := e.schedule[index]
		b := e.scenes[v.Scene].Track[track]
		elapsed := clock.SampleAtTick(tick) - clock.SampleAtTick(v.Tick+cycleStart)
		e.startClip(track, b.Clip, -1, elapsed)
	}
}
