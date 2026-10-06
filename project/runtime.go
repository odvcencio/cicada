package project

import (
	"fmt"
	"math"
	"slices"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// CompileEngine lowers a validated semantic project into immutable engine
// configuration. Compilation happens outside the audio callback; New copies
// every pattern and arrangement table before playback.
func CompileEngine(p *Project, sampleRate, maxBlock int) (engine.Config, error) {
	return compileEngine(p, sampleRate, maxBlock, nil, nil)
}

// CompileEngineWithAssets accepts immutable decoded PCM prepared by the host.
func CompileEngineWithAssets(p *Project, sampleRate, maxBlock int, assets []engine.AudioAsset) (engine.Config, error) {
	return compileEngine(p, sampleRate, maxBlock, assets, nil)
}

// CompileEnginePrepared accepts host-verified immutable native audio factories.
func CompileEnginePrepared(p *Project, sampleRate, maxBlock int, prepared []engine.StereoVoiceFactory) (engine.Config, error) {
	return compileEngine(p, sampleRate, maxBlock, nil, prepared)
}

func compileEngine(p *Project, sampleRate, maxBlock int, assets []engine.AudioAsset, prepared []engine.StereoVoiceFactory) (engine.Config, error) {
	var cfg engine.Config
	if err := ValidateProject(p); err != nil {
		return cfg, err
	}
	if p.NeedsSampleEngine() && len(prepared) != len(p.Tracks) && len(assets) != len(p.Assets) {
		return cfg, fmt.Errorf("CICADA-UNSUPPORTED: audio clips require prepared assets")
	}
	if len(prepared) == len(p.Tracks) && p.HasAudio() {
		copy := *p
		copy.Tracks = slices.Clone(p.Tracks)
		for i, track := range copy.Tracks {
			if p.Edition == 2 && track.Kind == "audio" {
				var err error
				copy.Tracks[i].Slots, err = ClipSlots(p, track)
				if err != nil {
					return cfg, err
				}
			}
		}
		p = &copy
	}
	if len(p.Scenes) > 1<<16 || len(p.Song) > 1<<16 {
		return cfg, fmt.Errorf("arrangement exceeds the kernel index range")
	}
	cfg = engine.Config{
		SampleRate: sampleRate, MaxBlock: maxBlock, Tracks: len(p.Tracks), MaxVoices: 32,
		BPMMilli: int64(p.TempoMilli), Seed: p.Seed,
		Patterns: make([]engine.PatternBank, len(p.Tracks)),
		Scenes:   make([]engine.Scene, len(p.Scenes)),
		Song:     make([]engine.SongEntry, len(p.Song)),
	}
	cfg.Assets = assets
	clipIndex := map[string]uint16{}
	assetIndex := map[string]uint16{}
	for i, a := range p.Assets {
		assetIndex[a.Name] = uint16(i)
	}
	for i, c := range p.Clips {
		clipIndex[c.Name] = uint16(i)
		if len(assets) > 0 {
			cfg.Clips = append(cfg.Clips, engine.ClipConfig{Asset: assetIndex[c.Asset], StartFrame: c.StartFrame, EndFrame: c.EndFrame, FadeInFrames: c.FadeInFrames, FadeOutFrames: c.FadeOutFrames, GainDB: c.GainDB})
		}
	}
	patterns := make(map[string]Pattern, len(p.Patterns))
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	programs := make(map[string]*instrument.Program, len(p.Instruments))
	for _, inst := range p.Instruments {
		program, err := compileInstrument(inst)
		if err != nil {
			return cfg, fmt.Errorf("instrument %s: %w", inst.ID, err)
		}
		programs[inst.ID] = program
	}
	kits := make(map[string]Kit, len(p.Kits))
	for _, kit := range p.Kits {
		kits[kit.ID] = kit
	}
	effectsByID := make(map[string]Effect, len(p.Effects))
	var delayParams *fx.DelayParams
	var reverbParams *fx.ReverbParams
	var compParams *fx.CompParams
	var compSidechain string
	for _, effect := range p.Effects {
		effectsByID[effect.ID] = effect
		if MasterHasInsert(p, effect.ID) {
			continue
		}
		kind := semanticEffectKind(effect)
		if kind == "delay" {
			params, err := DelayParamsFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			delayParams = &params
		} else if kind == "reverb" {
			params, err := ReverbParamsFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			reverbParams = &params
		} else if kind == "comp" {
			params, sidechain, err := CompSpecFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			compParams, compSidechain = &params, sidechain
		}
	}
	var masterErr error
	cfg.MasterProcessor, masterErr = PrepareMaster(p, sampleRate)
	if masterErr != nil {
		return cfg, masterErr
	}
	cfg.CompMusic = compParams
	cfg.DelayA = delayParams
	cfg.ReverbB = reverbParams
	for _, bus := range p.Buses {
		switch bus.ID {
		case "music":
			cfg.MusicBusMute, cfg.MusicBusSolo = bus.Mixer.Mute, bus.Mixer.Solo
		case "sfx":
			cfg.SFXBusMute, cfg.SFXBusSolo = bus.Mixer.Mute, bus.Mixer.Solo
		}
	}
	if p.Master != nil {
		cfg.MasterMute = p.Master.Mixer.Mute
		cfg.MasterSolo = p.Master.Mixer.Solo
		if p.Master.Mixer.Level != nil && p.Master.Mixer.Level.Number != nil && p.Master.Mixer.Level.Unit == "db" {
			cfg.MasterGainDB = *p.Master.Mixer.Level.Number
		}
	}
	if compSidechain == "sfx" {
		cfg.CompSidechainTrack = engine.SFXSidechain
	} else if compSidechain != "" && compSidechain != "music" {
		for index, track := range p.Tracks {
			if track.ID == compSidechain {
				cfg.CompSidechainTrack = index + 1
				break
			}
		}
	}
	trackIndex := make(map[string]int, len(p.Tracks))
	for ti, track := range p.Tracks {
		trackIndex[track.ID] = ti
		config := &cfg.Track[ti]
		config.GainDB, config.GainSet, config.Pan = track.Mixer.GainDB, true, track.Mixer.Pan
		config.Mute, config.Solo = track.Mixer.Mute, track.Mixer.Solo
		config.SendA, config.SendB, config.SendPre = track.Mixer.SendA, track.Mixer.SendB, track.Mixer.SendPre
		for _, send := range track.Mixer.Sends {
			effect, ok := effectsByID[send.To]
			if !ok || send.Level.Number == nil {
				continue
			}
			gain := *send.Level.Number
			if send.Level.Unit == "db" {
				gain = math.Pow(10, gain/20)
			}
			switch semanticEffectKind(effect) {
			case "delay":
				config.SendA = gain
				config.SendAPre = send.Tap == "pre"
			case "reverb":
				config.SendB = gain
				config.SendBPre = send.Tap == "pre"
			}
		}
		config.BusSFX = track.Mixer.Bus == "sfx"
		inserts := track.Mixer.Inserts
		if len(inserts) == 0 && track.Mixer.Insert != "none" {
			inserts = []string{track.Mixer.Insert}
		}
		if len(inserts) == 1 {
			if effect, ok := effectsByID[inserts[0]]; ok && semanticEffectKind(effect) == "drive" {
				params, err := DriveParamsFromValues(effect.Params)
				if err != nil {
					return cfg, err
				}
				config.InsertDrive = &params
			}
		}
		if len(prepared) == len(p.Tracks) && prepared[ti] != nil {
			config.Kind, config.Prepared = engine.VoicePrepared, prepared[ti]
			config.PreparedClip = p.Edition == 2 && track.Kind == "audio"
		} else {
			kind := track.Kind
			if kind == "piano" && !isModeledPiano(p, kind) || kind == "audio" && p.Edition != 2 {
				kind = "declared"
			}
			switch kind {
			case "piano":
				config.Kind = engine.VoicePiano
				sustain, err := PianoSustainFromValues(track.Params)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.PianoSustain = sustain
			case "guitar":
				config.Kind, config.Experimental = engine.VoiceGuitar, true
				params, err := guitarParamsFromValues(track.Params)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.Guitar = params
			case "audio":
				config.Kind = engine.VoiceAudio
			case "acid":
				config.Kind = engine.VoiceAcid
				params, err := acidParamsFromValues(track.Params)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.Acid = params
			case "drums":
				config.Kind = engine.VoiceDrums
				params, err := drumParamsFromValues(track.Params)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.Drums = params
				for lane, enabled := range projectDrumLanes(p, track) {
					if !enabled {
						config.Drums[lane] = drum.Params{}
					}
				}
				cfg.Patterns[ti].Drums = new([16][drum.LaneCount]seq.Pattern)
			default:
				if kit, ok := kits[track.Kind]; ok {
					config.Kind = engine.VoiceDrums
					bindings, err := CompileKitTrackAtSampleRate(kit, programs, track.Params, sampleRate)
					if err != nil {
						return cfg, fmt.Errorf("track %s: %w", track.ID, err)
					}
					config.Kit = bindings
					cfg.Patterns[ti].Drums = new([16][drum.LaneCount]seq.Pattern)
					break
				}
				for _, sampler := range p.Samplers {
					if sampler.Name == track.Kind {
						config.Kind = engine.VoiceSample
						config.Sample = &engine.SamplerConfig{Asset: assetIndex[sampler.Asset], RootKey: uint8(sampler.RootMIDI), Voices: uint8(sampler.Voices), Loop: sampler.Mode == "loop"}
						break
					}
				}
				if config.Kind == engine.VoiceSample {
					break
				}
				if profile, ok := modal.ParseTrackKind(track.Kind); ok {
					config.Kind, config.Modal = engine.VoiceModal, profile
					break
				}
				config.Kind = engine.VoiceGraph
				program := programs[track.Kind]
				if program == nil {
					return cfg, fmt.Errorf("track %s has no compiled instrument", track.ID)
				}
				overrides := map[string]string{}
				for name, value := range track.Params {
					if name == "octave" && !program.HasParameter("octave") {
						continue
					}
					literal, err := valueSource(value)
					if err != nil {
						return cfg, fmt.Errorf("track %s parameter %s: %w", track.ID, name, err)
					}
					overrides[name] = literal
				}
				graph, err := instrument.Lower(program, overrides)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.Graph = graph
				if program.Mode == "poly" {
					if GraphPolyphony(p, track.Kind) == 4 {
						config.Polyphony = 4
					} else {
						config.Kind = engine.VoiceGraphPoly
					}
				}
			}
		}
		for slot, patternID := range track.Slots {
			if config.Kind == engine.VoiceAudio || patternID == nil {
				continue
			}
			if config.PreparedClip {
				cfg.Patterns[ti].Slots[slot] = seq.Pattern{Len: 16, GatePercent: 50, Seed: p.Seed}
				continue
			}
			pattern := patterns[*patternID]
			base, err := kernelPattern(pattern)
			if err != nil {
				return cfg, fmt.Errorf("pattern %s: %w", pattern.ID, err)
			}
			if config.Kind == engine.VoiceDrums {
				if pattern.Transpose != 0 {
					return cfg, fmt.Errorf("drum pattern %s cannot transpose lanes", pattern.ID)
				}
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					compiled := base
					for step, source := range pattern.Lanes[drum.Names[lane]] {
						compiled.Steps[step], err = packProjectStep(source, true, uint8(lane))
						if err != nil {
							return cfg, fmt.Errorf("pattern %s lane %s: %w", pattern.ID, drum.Names[lane], err)
						}
					}
					cfg.Patterns[ti].Drums[slot][lane] = compiled
				}
			} else {
				for step, source := range pattern.Data {
					if source != nil && len(source.Notes) > 0 {
						base.Chords[step].Count = uint8(len(source.Notes))
						for i, note := range source.Notes {
							base.Chords[step].Notes[i] = uint8(note)
						}
					}
					base.Steps[step], err = packProjectStep(source, false, 0)
					if err != nil {
						return cfg, fmt.Errorf("pattern %s: %w", pattern.ID, err)
					}
				}
			}
			if err := base.Validate(); err != nil {
				return cfg, fmt.Errorf("pattern %s: %w", pattern.ID, err)
			}
			if config.Kind == engine.VoiceGraph || config.Kind == engine.VoiceGraphPoly {
				if err := ValidateGraphDelayPattern(config.Graph, sampleRate, base); err != nil {
					return cfg, fmt.Errorf("CICADA-PARAM: track %s pattern %s: %w", track.ID, pattern.ID, err)
				}
			}
			cfg.Patterns[ti].Slots[slot] = base
		}
	}
	sceneIndex := make(map[string]uint16, len(p.Scenes))
	for si, scene := range p.Scenes {
		sceneIndex[scene.ID] = uint16(si)
		for trackID, patternID := range scene.Bindings {
			ti := trackIndex[trackID]
			binding := &cfg.Scenes[si].Track[ti]
			switch patternID {
			case "keep":
				binding.Mode = engine.SceneKeep
			case "off":
				binding.Mode = engine.SceneOff
			default:
				if cfg.Track[ti].Kind == engine.VoiceAudio {
					binding.Mode = engine.SceneClip
					binding.Clip = clipIndex[patternID]
					continue
				}
				binding.Mode = engine.SceneSlot
				for slot, stored := range p.Tracks[ti].Slots {
					if stored != nil && *stored == patternID {
						binding.Slot = uint8(slot)
						break
					}
				}
			}
		}
		var err error
		cfg.Scenes[si].Settings, err = CompileSceneSettings(p, scene)
		if err != nil {
			return cfg, err
		}
	}
	for i, entry := range p.Song {
		cfg.Song[i] = engine.SongEntry{Scene: sceneIndex[entry.Scene], Bars: entry.Bars}
	}
	if p.Arrange != nil {
		cfg.Song = nil
		var scheduleErr error
		cfg.Schedule, scheduleErr = CompileSchedule(p)
		if scheduleErr != nil {
			return cfg, scheduleErr
		}
	}
	return cfg, nil
}

// CompileSceneSettings resolves scene settings in source order to the same
// float32 values and delay divisions for playback and offline rendering.
func CompileSceneSettings(p *Project, scene Scene) ([]engine.SceneSetting, error) {
	settings := make([]engine.SceneSetting, 0, len(scene.Settings))
	for _, setting := range scene.Settings {
		resolved, err := ResolveParameterPath(p, setting.Path)
		if err != nil {
			return nil, err
		}
		value, division, err := sceneSettingKernelValue(p, setting, resolved)
		if err != nil {
			return nil, fmt.Errorf("scene %s path %s: %w", scene.ID, setting.Path, err)
		}
		settings = append(settings, engine.SceneSetting{Track: resolved.Track, ID: resolved.ID, Value: value, Division: division})
	}
	return settings, nil
}

func sceneSettingKernelValue(p *Project, setting SceneSetting, resolved ResolvedParam) (float32, fx.DelayDivision, error) {
	value := setting.Value.projectValue()
	if value.Unit == "enum" {
		if value.Text == "off" && resolved.Descriptor.Off {
			return float32(math.Inf(-1)), fx.FreeDelay, nil
		}
		if resolved.Descriptor.ID == "fx.delay.time" {
			division, err := fx.ParseDelayDivision(value.Text)
			if err != nil || division == fx.FreeDelay {
				return 0, fx.FreeDelay, fmt.Errorf("CICADA-UNSUPPORTED: scene setting %s value %q has no engine representation", setting.Path, value.Text)
			}
			for _, effect := range p.Effects {
				if effect.ID != resolved.Owner {
					continue
				}
				params, err := DelayParamsFromValues(effect.Params)
				if err != nil {
					return 0, fx.FreeDelay, err
				}
				params.Division, params.TimeMs = division, 0
				if err := params.ValidateTempo(int64(p.TempoMilli)); err != nil {
					return 0, fx.FreeDelay, fmt.Errorf("CICADA-PARAM: scene setting %s value %q: %w", setting.Path, value.Text, err)
				}
				return 0, division, nil
			}
			return 0, fx.FreeDelay, fmt.Errorf("CICADA-REFERENCE: scene setting %s has no delay effect", setting.Path)
		}
		if resolved.Descriptor.Curve == "toggle" {
			switch value.Text {
			case "false":
				return 0, fx.FreeDelay, nil
			case "true":
				return 1, fx.FreeDelay, nil
			}
		}
		if value.Text == "auto" && resolved.Descriptor.ID == "fx.comp.makeup" {
			for _, effect := range p.Effects {
				if effect.ID == resolved.Owner {
					return float32(automaticMakeup(effect.Params)), fx.FreeDelay, nil
				}
			}
		}
		return 0, fx.FreeDelay, fmt.Errorf("CICADA-UNSUPPORTED: scene setting %s value %q has no engine representation", setting.Path, value.Text)
	}
	if value.Number == nil {
		return 0, fx.FreeDelay, fmt.Errorf("setting has no numeric value")
	}
	compiled, err := sceneSettingFloat32Value(resolved.Descriptor.Min, resolved.Descriptor.Max, *value.Number)
	if err != nil {
		return 0, fx.FreeDelay, err
	}
	return compiled, fx.FreeDelay, nil
}

func sceneSettingFloat32Value(minimum, maximum, value float64) (float32, error) {
	compiled := float32(value)
	if float64(compiled) < minimum {
		if value != minimum {
			return 0, fmt.Errorf("value rounds outside float32 range %g..%g", minimum, maximum)
		}
		compiled = math.Nextafter32(compiled, float32(math.Inf(1)))
	}
	if float64(compiled) > maximum {
		if value != maximum {
			return 0, fmt.Errorf("value rounds outside float32 range %g..%g", minimum, maximum)
		}
		compiled = math.Nextafter32(compiled, float32(math.Inf(-1)))
	}
	if float64(compiled) < minimum || float64(compiled) > maximum {
		return 0, fmt.Errorf("float32 boundary step is outside range %g..%g", minimum, maximum)
	}
	return compiled, nil
}

func kernelPattern(pattern Pattern) (seq.Pattern, error) {
	swing, err := seq.SwingFromPercent100(pattern.SwingPercent100)
	if err != nil {
		return seq.Pattern{}, err
	}
	return seq.Pattern{
		Len: pattern.Steps, SwingPermille: swing, GatePercent: pattern.GatePercent,
		Transpose: pattern.Transpose, Seed: pattern.Seed,
	}, nil
}

func packProjectStep(source *Step, isDrum bool, drumLane uint8) (uint32, error) {
	if source == nil {
		return seq.PackStep(seq.Step{Note: drumLane, Ratchet: 1, Probability: 100})
	}
	note := source.Note
	if isDrum {
		note = drumLane
	}
	return seq.PackStep(seq.Step{
		Note: note, Accent: source.Accent, Slide: source.Slide, Gate: true,
		Tie: source.Tie, Ratchet: source.Ratchet, Probability: source.Probability, Velocity: source.Velocity,
	})
}
