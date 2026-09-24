package project

import (
	"fmt"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// CompileEngine lowers a validated semantic project into immutable engine
// configuration. Compilation happens outside the audio callback; New copies
// every pattern and arrangement table before playback.
func CompileEngine(p *Project, sampleRate, maxBlock int) (engine.Config, error) {
	var cfg engine.Config
	if err := ValidateProject(p); err != nil {
		return cfg, err
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
	var driveParams *fx.DriveParams
	var delayParams *fx.DelayParams
	var reverbParams *fx.ReverbParams
	var compParams *fx.CompParams
	var compSidechain string
	for _, effect := range p.Effects {
		if effect.ID == "drive" {
			params, err := DriveParamsFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			driveParams = &params
		} else if effect.ID == "delay" {
			params, err := DelayParamsFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			delayParams = &params
		} else if effect.ID == "reverb" {
			params, err := ReverbParamsFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			reverbParams = &params
		} else if effect.ID == "comp" {
			params, sidechain, err := CompSpecFromValues(effect.Params)
			if err != nil {
				return cfg, err
			}
			compParams, compSidechain = &params, sidechain
		}
	}
	cfg.CompMusic = compParams
	if compSidechain != "" && compSidechain != "music" {
		for index, track := range p.Tracks {
			if track.ID == compSidechain {
				cfg.CompSidechainTrack = index + 1
				break
			}
		}
	}
	for _, track := range p.Tracks {
		if track.Mixer.SendA > 0 {
			cfg.DelayA = delayParams
		}
		if track.Mixer.SendB > 0 {
			cfg.ReverbB = reverbParams
		}
	}
	trackIndex := make(map[string]int, len(p.Tracks))
	for ti, track := range p.Tracks {
		trackIndex[track.ID] = ti
		config := &cfg.Track[ti]
		config.GainDB, config.GainSet, config.Pan, config.Mute = track.Mixer.GainDB, true, track.Mixer.Pan, track.Mixer.Mute
		config.SendA, config.SendB, config.SendPre = track.Mixer.SendA, track.Mixer.SendB, track.Mixer.SendPre
		if track.Mixer.Insert == "drive" {
			config.InsertDrive = driveParams
		}
		switch track.Kind {
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
			cfg.Patterns[ti].Drums = new([16][drum.LaneCount]seq.Pattern)
		default:
			if kit, ok := kits[track.Kind]; ok {
				config.Kind = engine.VoiceDrums
				bindings, err := CompileKit(kit, programs)
				if err != nil {
					return cfg, fmt.Errorf("track %s: %w", track.ID, err)
				}
				config.Kit = bindings
				cfg.Patterns[ti].Drums = new([16][drum.LaneCount]seq.Pattern)
				break
			}
			config.Kind = engine.VoiceGraph
			program := programs[track.Kind]
			if program == nil {
				return cfg, fmt.Errorf("track %s has no compiled instrument", track.ID)
			}
			overrides := map[string]string{}
			for name, value := range track.Params {
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
		}
		for slot, patternID := range track.Slots {
			if patternID == nil {
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
					base.Steps[step], err = packProjectStep(source, false, 0)
					if err != nil {
						return cfg, fmt.Errorf("pattern %s: %w", pattern.ID, err)
					}
				}
			}
			if err := base.Validate(); err != nil {
				return cfg, fmt.Errorf("pattern %s: %w", pattern.ID, err)
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
				binding.Mode = engine.SceneSlot
				for slot, stored := range p.Tracks[ti].Slots {
					if stored != nil && *stored == patternID {
						binding.Slot = uint8(slot)
						break
					}
				}
			}
		}
	}
	for i, entry := range p.Song {
		cfg.Song[i] = engine.SongEntry{Scene: sceneIndex[entry.Scene], Bars: entry.Bars}
	}
	return cfg, nil
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
