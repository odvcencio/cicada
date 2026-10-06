package sampleasset

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/project"
)

// Prepare verifies and decodes each referenced asset once, before constructing
// audio owners. The aggregate budget is 64 MiB of resident planar float32 PCM.
// Factories share immutable PCM, never playback state.
func Prepare(dir string, p *project.Project) ([]engine.StereoVoiceFactory, error) {
	if err := project.ValidateProject(p); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, fmt.Errorf("audio preparation requires a project directory")
	}
	assets := map[string]project.Asset{}
	for _, asset := range p.Assets {
		assets[asset.Name] = asset
	}
	regions := map[string]sample.Region{}
	var resident int64
	load := func(name string) (sample.Region, error) {
		if region, ok := regions[name]; ok {
			return region, nil
		}
		asset, ok := assets[name]
		if !ok {
			return sample.Region{}, fmt.Errorf("unknown sample asset %s", name)
		}
		bytes := asset.Frames * int64(asset.Channels) * 4
		if asset.Frames > MaxFrames || bytes <= 0 || bytes > 64<<20-resident {
			return sample.Region{}, fmt.Errorf("project audio exceeds the 64 MiB resident PCM budget")
		}
		region, err := LoadRegion(dir, asset, 0, 0, 60, false)
		if err != nil {
			return region, err
		}
		resident += bytes
		regions[name] = region
		return region, nil
	}
	clips := map[string]project.Clip{}
	for _, clip := range p.Clips {
		clips[clip.Name] = clip
	}
	samplers := map[string]project.Sampler{}
	for _, sampler := range p.Samplers {
		if sampler.Pack == "" {
			samplers[sampler.Name] = sampler
		}
	}
	prepared := make([]engine.StereoVoiceFactory, len(p.Tracks))
	for ti, track := range p.Tracks {
		if p.Edition == 2 && track.Kind == "audio" {
			factory := &clipFactory{}
			slots, err := project.ClipSlots(p, track)
			if err != nil {
				return nil, err
			}
			for slot, id := range slots {
				if id == nil {
					continue
				}
				clip, ok := clips[*id]
				if !ok {
					return nil, fmt.Errorf("track %s has unknown clip %s", track.ID, *id)
				}
				region, err := load(clip.Asset)
				if err != nil {
					return nil, fmt.Errorf("clip %s: %w", clip.Name, err)
				}
				region.Start, region.End = int(clip.StartFrame), int(clip.EndFrame)
				factory.slots[slot] = &clipRegion{region: region, gain: math.Pow(10, clip.GainDB/20), fadeIn: clip.FadeInFrames, fadeOut: clip.FadeOutFrames}
			}
			prepared[ti] = factory
		} else if sampler, ok := samplers[track.Kind]; ok {
			region, err := load(sampler.Asset)
			if err != nil {
				return nil, fmt.Errorf("sampler %s: %w", sampler.Name, err)
			}
			region.RootKey, region.Loop = uint8(sampler.RootMIDI), sampler.Mode == "loop"
			prepared[ti] = &samplerFactory{region: region, voices: sampler.Voices}
		}
	}
	return prepared, nil
}

// CompileEngine is the native host entrypoint. Synth scores take the original
// compiler path; filesystem access never reaches the kernel callback.
func CompileEngine(dir string, p *project.Project, rate, block int) (engine.Config, error) {
	if p == nil {
		return engine.Config{}, fmt.Errorf("sample project is missing")
	}
	if !p.HasAudio() {
		return project.CompileEngine(p, rate, block)
	}
	prepared, err := Prepare(dir, p)
	if err != nil {
		return engine.Config{}, err
	}
	if err := ValidatePatterns(p, prepared, rate); err != nil {
		return engine.Config{}, err
	}
	return project.CompileEnginePrepared(p, rate, block, prepared)
}

// ValidatePatterns checks source note ratios before publishing an engine or a
// WAV header. Unsupported pitches cannot fault a running render callback.
func ValidatePatterns(p *project.Project, factories []engine.StereoVoiceFactory, rate int) error {
	if len(factories) != len(p.Tracks) {
		return fmt.Errorf("prepared audio table differs from the project")
	}
	patterns := map[string]project.Pattern{}
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	for i, track := range p.Tracks {
		if factories[i] == nil || p.Edition == 2 && track.Kind == "audio" {
			continue
		}
		voice, err := factories[i].NewStereoVoice(rate)
		if err != nil {
			return err
		}
		for _, slot := range track.Slots {
			if slot == nil {
				continue
			}
			pattern := patterns[*slot]
			for _, step := range pattern.Data {
				if step == nil || step.Tie {
					continue
				}
				note := max(0, min(127, int(step.Note)+int(pattern.Transpose)))
				if err := voice.NoteOn(uint8(note), step.Velocity); err != nil {
					return fmt.Errorf("sampler track %s, pattern %s, MIDI note %d: %w", track.ID, *slot, note, err)
				}
			}
		}
	}
	return nil
}

type samplerFactory struct {
	region sample.Region
	voices int
}

func (f *samplerFactory) VoiceCount() int { return f.voices }
func (f *samplerFactory) NewStereoVoice(rate int) (engine.StereoVoice, error) {
	pool, err := sample.NewPool(rate, f.voices, f.region)
	if err != nil {
		return nil, err
	}
	return &samplerVoice{pool: pool}, nil
}

type samplerVoice struct{ pool *sample.Pool }

func (v *samplerVoice) NoteOn(note, velocity uint8) error {
	_, err := v.pool.NoteOn(note, velocity)
	return err
}
func (v *samplerVoice) NoteOff()                            { v.pool.NoteOffAll() }
func (v *samplerVoice) Reset()                              { v.pool.Reset() }
func (v *samplerVoice) SelectSlot(uint8, int64, bool) error { return nil }
func (v *samplerVoice) Play() error                         { return nil }
func (v *samplerVoice) NextStereo() (float32, float32)      { return v.pool.NextStereo() }

type clipRegion struct {
	region          sample.Region
	gain            float64
	fadeIn, fadeOut int64
}
type clipFactory struct{ slots [16]*clipRegion }

func (f *clipFactory) VoiceCount() int { return 1 }
func (f *clipFactory) NewStereoVoice(rate int) (engine.StereoVoice, error) {
	v := &clipVoice{factory: f, selected: -1, tailFrames: rate / 500}
	for slot, clip := range f.slots {
		if clip == nil {
			continue
		}
		voice, err := sample.New(rate, clip.region)
		if err != nil {
			return nil, err
		}
		if err := voice.SetParams(sample.Params{Gain: clip.gain}); err != nil {
			return nil, err
		}
		v.voices[slot] = voice
	}
	return v, nil
}

type clipVoice struct {
	factory                    *clipFactory
	voices                     [16]*sample.Voice
	position                   [16]int64
	selected                   int
	playing                    bool
	lastL, lastR, tailL, tailR float32
	tailRemaining, tailFrames  int
}

func (v *clipVoice) SelectSlot(slot uint8, elapsed int64, playing bool) error {
	if slot >= 16 || v.voices[slot] == nil || elapsed < 0 {
		return sample.Error("audio clip slot or offset is invalid")
	}
	v.NoteOff()
	v.selected, v.position[slot], v.playing = int(slot), elapsed, playing
	if playing {
		return v.voices[slot].NoteOnAt(60, 127, elapsed)
	}
	return nil
}
func (v *clipVoice) Play() error {
	if v.playing || v.selected < 0 {
		return nil
	}
	v.playing = true
	return v.voices[v.selected].NoteOnAt(60, 127, v.position[v.selected])
}
func (v *clipVoice) NoteOn(uint8, uint8) error {
	return sample.Error("audio clips are selected by scene, not MIDI notes")
}
func (v *clipVoice) NoteOff() {
	if v.playing {
		v.tailL, v.tailR, v.tailRemaining = v.lastL, v.lastR, v.tailFrames
		if v.selected >= 0 {
			v.voices[v.selected].Reset()
		}
	}
	v.playing = false
}
func (v *clipVoice) Reset() {
	for _, voice := range v.voices {
		if voice != nil {
			voice.Reset()
		}
	}
	v.position, v.selected, v.playing = [16]int64{}, -1, false
	v.lastL, v.lastR, v.tailL, v.tailR, v.tailRemaining = 0, 0, 0, 0, 0
}
func (v *clipVoice) NextStereo() (float32, float32) {
	var left, right float64
	if v.selected >= 0 && v.playing {
		slot, voice := v.selected, v.voices[v.selected]
		l, r := voice.NextStereo()
		clip := v.factory.slots[slot]
		frame := float64(v.position[slot]) * voice.Ratio()
		gain := float64(1)
		if clip.fadeIn > 0 {
			gain = min(gain, frame/float64(clip.fadeIn))
		}
		if clip.fadeOut > 0 {
			gain = min(gain, (float64(clip.region.End-clip.region.Start)-frame)/float64(clip.fadeOut))
		}
		gain = max(0, gain)
		left += float64(l) * gain
		right += float64(r) * gain
		v.position[slot]++
	}
	if v.tailRemaining > 0 {
		gain := float64(v.tailRemaining) / float64(v.tailFrames)
		left += float64(v.tailL) * gain
		right += float64(v.tailR) * gain
		v.tailRemaining--
	}
	v.lastL, v.lastR = float32(left), float32(right)
	return v.lastL, v.lastR
}
