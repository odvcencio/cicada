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
		frames := ((c.EndFrame-c.StartFrame)*int64(cfg.SampleRate) + int64(a.SampleRate) - 1) / int64(a.SampleRate)
		e.clipMaxFrames = max(e.clipMaxFrames, frames)
	}
	return nil
}
func (e *Engine) startClip(track int, clip uint16, id int, elapsed int64) {
	if int(clip) >= len(e.clipTemplates) {
		e.fault(17)
		return
	}
	voice := e.clipTemplates[clip]
	if voice.NoteOn(60, 127) != nil {
		e.fault(17)
		return
	}
	voice.SeekFrames(elapsed)
	if !voice.Active() {
		return
	}
	for i := range e.clipVoices[:e.clipLimit] {
		v := &e.clipVoices[i]
		if v.active {
			continue
		}
		*v = clipPlayback{voice: voice, track: track, clip: clip, id: id, active: true}
		v.ratio = v.voice.Ratio()
		v.sourceFrame = float64(elapsed) * v.ratio
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
	clock := seq.Clock{SampleRate: int64(e.sampleRate), BPMMilli: e.transport.BPMMilli()}
	sample := clock.SampleAtTick(tick)
	startCycle := int64(0)
	if e.loopSong {
		oldestTick := clock.TickAtSample(max(0, sample-e.clipMaxFrames))
		startCycle = oldestTick / e.scheduleDuration * e.scheduleDuration
	}
	// Replay starts and stops in source order. Clips can overlap scene entries
	// and loop cycles; natural completion is checked before claiming a voice.
	for cycle := startCycle; cycle <= cycleStart; cycle += e.scheduleDuration {
		last := len(e.schedule) - 1
		if cycle == cycleStart {
			last = index
		}
		for i := 0; i <= last && !e.faulted; i++ {
			event := e.schedule[i]
			elapsed := sample - clock.SampleAtTick(event.Tick+cycle)
			for track, binding := range e.scenes[event.Scene].Track {
				if track >= e.tracks || e.voices[track].kind != VoiceAudio {
					continue
				}
				switch binding.Mode {
				case SceneClip:
					e.startClip(track, binding.Clip, -1, elapsed)
				case SceneOff:
					e.stopClips(track)
				}
			}
		}
	}
}
