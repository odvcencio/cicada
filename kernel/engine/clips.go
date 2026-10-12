package engine

import (
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/sample"
	"math"
	"math/bits"
)

// AudioAsset is immutable decoded planar PCM, prepared by a host worker.
type AudioAsset struct {
	Left, Right []float32
	SampleRate  int
	StreamID    uint32 `json:",omitempty"`
	Frames      int64  `json:",omitempty"`
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

// clipSlotMask indexes playback slots in ascending order for one track. Starts
// and reassignments run at control boundaries; only the owning raw DSP track
// prunes finished slots during rendering.
type clipSlotMask [(MaxVoices + 63) / 64]uint64

func (m *clipSlotMask) add(slot int)    { m[slot/64] |= uint64(1) << (slot % 64) }
func (m *clipSlotMask) remove(slot int) { m[slot/64] &^= uint64(1) << (slot % 64) }

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
	e.copyClipAssets(cfg)
	for i, c := range cfg.Clips {
		if int(c.Asset) >= len(cfg.Assets) || c.StartFrame < 0 || c.EndFrame <= c.StartFrame || c.EndFrame > 1<<30 || c.FadeInFrames < 0 || c.FadeOutFrames < 0 || c.FadeInFrames > c.EndFrame-c.StartFrame || c.FadeOutFrames > c.EndFrame-c.StartFrame || math.IsNaN(c.GainDB) || math.IsInf(c.GainDB, 0) || c.GainDB < -60 || c.GainDB > 24 {
			return Error("clip region is invalid")
		}
		a := cfg.Assets[c.Asset]
		region := sample.Region{Left: a.Left, Right: a.Right, SampleRate: a.SampleRate, RootKey: 60, Start: int(c.StartFrame), End: int(c.EndFrame)}
		voice, err := prepareClipVoice(cfg, a, c, region)
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
		e.fault(FaultClipStart)
		return
	}
	for i := range e.clipVoices[:e.clipLimit] {
		v := &e.clipVoices[i]
		if v.active {
			continue
		}
		voice := e.clipTemplates[clip]
		if e.bindClipSource(&voice, i, clip) != nil {
			e.fault(FaultClipStart)
			return
		}
		if voice.NoteOn(60, 127) != nil {
			e.fault(FaultClipStart)
			return
		}
		voice.SeekFrames(elapsed)
		if !voice.Active() {
			voice.Reset()
			return
		}
		// A scheduled end may leave an inactive slot indexed until its track
		// renders again. Remove that membership before another track reuses it.
		e.clipSlots[v.track].remove(i)
		*v = clipPlayback{voice: voice, track: track, clip: clip, id: id, active: true}
		e.clipSlots[track].add(i)
		v.ratio = v.voice.Ratio()
		v.sourceFrame = float64(elapsed) * v.ratio
		return
	}
	e.fault(FaultClipStart)
}
func (e *Engine) stopClips(track int) {
	for i := range e.clipVoices {
		v := &e.clipVoices[i]
		if track < 0 || v.track == track {
			v.voice.Reset()
			v.active = false
			e.clipSlots[v.track].remove(i)
		}
	}
}
func (e *Engine) resetClips() {
	e.resetClipReaders()
	e.clipVoices = [MaxVoices]clipPlayback{}
	e.clipSlots = [MaxTracks]clipSlotMask{}
}
func (e *Engine) nextClipStereo(track int) (float32, float32) {
	// Frozen before raw DSP: a failed worker can outlive transport faulting.
	if !e.rawPlaying {
		return 0, 0
	}
	var left, right float64
	index := &e.clipSlots[track]
	for word, mask := range index {
		for mask != 0 {
			i := word*64 + bits.TrailingZeros64(mask)
			mask &= mask - 1
			v := &e.clipVoices[i]
			if !v.active {
				index.remove(i)
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
			if !v.active {
				index.remove(i)
				e.finishClipVoice(v)
			}
		}
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
	// Replay starts and stops in source order. Explicit clip assignments
	// retrigger that track; keep carries its clip across scenes and loop cycles.
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
					e.stopClips(track)
					e.startClip(track, binding.Clip, -1, elapsed)
				case SceneOff:
					e.stopClips(track)
				}
			}
		}
	}
}
