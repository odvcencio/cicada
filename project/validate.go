package project

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/piano"
	"m31labs.dev/cicada/notation"
)

var acceptedScales = map[string]bool{
	"minor": true, "major": true, "dorian": true, "phrygian": true,
	"harmonic": true, "pent": true, "mixo": true, "blues": true,
}

func validateOctaveValue(value Value) error {
	if value.Unit != "unit" || value.Number == nil || !finite(*value.Number) || *value.Number < 0 || *value.Number > 6 || math.Trunc(*value.Number) != *value.Number {
		return fmt.Errorf("octave must be a unitless integer 0 to 6")
	}
	return nil
}

// ValidateProject checks the semantic interchange records after source lowering
// or JSON decoding. It does not yet load audio into the runtime engine.
func ValidateProject(p *Project) error {
	if p == nil || !((p.Format == FormatID && p.Version == 1) || (p.Format == FormatID2 && p.Version == 2)) {
		return fmt.Errorf("unsupported project format or version")
	}
	if err := validateAudioProject(p); err != nil {
		return err
	}
	if p.Live != nil {
		if p.Edition != 2 || p.Format != FormatID2 {
			return fmt.Errorf("CICADA-VERSION: live controls require edition 2 and cicada.project/2")
		}
		if p.Live.Macros == nil || p.Live.Layers == nil {
			return fmt.Errorf("CICADA-LIVE-BLOCK: live arrays must be explicit")
		}
		for _, layers := range p.Live.Layers {
			if layers.Rules == nil {
				return fmt.Errorf("CICADA-LIVE-BLOCK: layer rules must be explicit")
			}
		}
		tracks := make([]notation.Track, len(p.Tracks))
		for i, track := range p.Tracks {
			tracks[i].Name = track.ID
		}
		if ds := notation.ValidateLive(liveToScore(p.Live), tracks); len(ds) > 0 {
			return fmt.Errorf("%s: %s", ds[0].Code, ds[0].Message)
		}
		if err := validateDirectorProject(p, tracks); err != nil {
			return err
		}
		for _, macro := range p.Live.Macros {
			if !validID(macro.Name) {
				return fmt.Errorf("CICADA-LIVE-MACRO: invalid macro name %s", macro.Name)
			}
		}
	}
	if p.Format == FormatID && projectHasSceneSettings(p) {
		return fmt.Errorf("scene settings require cicada.project/2")
	}
	if p.Edition != 1 && p.Edition != 2 {
		return fmt.Errorf("CICADA-VERSION: only cicada 1 and 2 are supported")
	}
	if p.Format == FormatID && (len(p.Buses) > 0 || p.Master != nil || len(p.Exports) > 0) {
		return fmt.Errorf("cicada.project/2 is required for buses, master, and exports")
	}
	if !utf8.ValidString(p.Title) || utf8.RuneCountInString(p.Title) > 120 {
		return fmt.Errorf("title must contain at most 120 UTF-8 characters")
	}
	if p.TempoMilli < 20_000 || p.TempoMilli > 300_000 || p.Key.Root > 11 || !acceptedScales[p.Key.Scale] {
		return fmt.Errorf("invalid tempo or key")
	}
	if p.Instruments == nil || p.Kits == nil || p.Tracks == nil || p.Patterns == nil || p.Scenes == nil || p.Song == nil || p.Effects == nil {
		return fmt.Errorf("project arrays must be explicit")
	}
	effects := map[string]Effect{}
	kindCounts := map[string]int{}
	var compSidechain string
	for _, effect := range p.Effects {
		kind := semanticEffectKind(effect)
		if kind != "drive" && kind != "delay" && kind != "reverb" && kind != "comp" && kind != "eq" && kind != "transient" && kind != "width" && kind != "limiter" && kind != "convolution" {
			return fmt.Errorf("CICADA-UNSUPPORTED: effect kind %s is not implemented", kind)
		}
		if effect.ID == "music" || effect.ID == "sfx" || !validID(effect.ID) {
			return fmt.Errorf("effect %s conflicts with a built-in bus or has an invalid ID", effect.ID)
		}
		if _, exists := effects[effect.ID]; exists || effect.Params == nil {
			return fmt.Errorf("duplicate or incomplete effect %s", effect.ID)
		}
		effects[effect.ID] = effect
		if !MasterHasInsert(p, effect.ID) {
			kindCounts[kind]++
		}
		if kind == "eq" || kind == "transient" || kind == "width" || kind == "limiter" || kind == "convolution" {
			if !MasterHasInsert(p, effect.ID) {
				return fmt.Errorf("CICADA-UNSUPPORTED: effect %s must be inserted on master", effect.ID)
			}
		}
		if kind == "drive" {
			if _, err := DriveParamsFromValues(effect.Params); err != nil {
				return fmt.Errorf("effect %s: %w", effect.ID, err)
			}
		} else if kind == "delay" {
			params, err := DelayParamsFromValues(effect.Params)
			if err != nil {
				return fmt.Errorf("effect %s: %w", effect.ID, err)
			}
			if err := params.ValidateTempo(int64(p.TempoMilli)); err != nil {
				return fmt.Errorf("effect %s: %w", effect.ID, err)
			}
		} else if kind == "reverb" {
			if _, err := ReverbParamsFromValues(effect.Params); err != nil {
				return fmt.Errorf("effect %s: %w", effect.ID, err)
			}
		} else if kind == "comp" {
			var err error
			if _, compSidechain, err = CompSpecFromValues(effect.Params); err != nil {
				return fmt.Errorf("effect %s: %w", effect.ID, err)
			}
		}
	}
	if kindCounts["delay"] > 1 || kindCounts["reverb"] > 1 {
		return fmt.Errorf("CICADA-UNSUPPORTED: multiple delay or reverb instances are not implemented")
	}
	if len(p.Tracks) < 1 || len(p.Tracks) > 16 || len(p.Patterns) == 0 && len(p.Clips) == 0 || len(p.Song) == 0 {
		return fmt.Errorf("project needs 1 to 16 tracks, patterns, and a song")
	}
	instruments := map[string]*instrument.Program{}
	trackGraphs := map[string]graph.Program{}
	seenInstruments := map[string]bool{}
	for _, inst := range p.Instruments {
		if p.Edition == 2 && inst.ID == "audio" {
			return fmt.Errorf("instrument name is reserved: %s", inst.ID)
		}
		if inst.Octave == nil || *inst.Octave < 0 || *inst.Octave > 6 {
			return fmt.Errorf("instrument %s octave must be 0 to 6", inst.ID)
		}
		if err := uniqueID(inst.ID, seenInstruments); err != nil {
			return fmt.Errorf("instrument: %w", err)
		}
		if inst.Mode != "mono" && inst.Mode != "poly" || inst.Params == nil || inst.Lets == nil {
			return fmt.Errorf("instrument %s uses an unsupported voice mode or incomplete fields", inst.ID)
		}
		params := map[string]bool{}
		for _, param := range inst.Params {
			if err := uniqueID(param.ID, params); err != nil {
				return fmt.Errorf("instrument %s parameter: %w", inst.ID, err)
			}
			if !validNumericUnit(param.Unit) || !finite(param.Default) {
				return fmt.Errorf("instrument %s parameter %s has invalid unit or value", inst.ID, param.ID)
			}
		}
		bindings := map[string]bool{}
		for _, binding := range inst.Lets {
			if err := uniqueID(binding.ID, bindings); err != nil {
				return fmt.Errorf("instrument %s binding: %w", inst.ID, err)
			}
			if _, err := validateExpr(binding.Value, 0); err != nil {
				return fmt.Errorf("instrument %s binding %s: %w", inst.ID, binding.ID, err)
			}
		}
		if _, err := validateExpr(inst.Out, 0); err != nil {
			return fmt.Errorf("instrument %s output: %w", inst.ID, err)
		}
		program, err := compileInstrument(inst)
		if err != nil {
			return fmt.Errorf("instrument %s: %w", inst.ID, err)
		}
		instruments[inst.ID] = program
	}
	kits := map[string]Kit{}
	for _, kit := range p.Kits {
		if !validID(kit.ID) || kit.ID == "acid" || kit.ID == "drums" || p.Edition == 2 && kit.ID == "audio" || instruments[kit.ID] != nil {
			return fmt.Errorf("kit %s has an invalid or reserved ID", kit.ID)
		}
		if _, exists := kits[kit.ID]; exists {
			return fmt.Errorf("duplicate kit %s", kit.ID)
		}
		if _, err := CompileKit(kit, instruments); err != nil {
			return err
		}
		kits[kit.ID] = kit
	}
	samplers := map[string]Sampler{}
	for _, sampler := range p.Samplers {
		samplers[sampler.Name] = sampler
	}
	clips := map[string]Clip{}
	for _, clip := range p.Clips {
		clips[clip.Name] = clip
	}
	tracks := map[string]Track{}
	for _, track := range p.Tracks {
		if _, exists := tracks[track.ID]; exists || !validID(track.ID) || track.ID == "music" || track.ID == "sfx" {
			return fmt.Errorf("duplicate or invalid track ID %q", track.ID)
		}
		tracks[track.ID] = track
		_, isKit := kits[track.Kind]
		if track.Kind != "acid" && track.Kind != "drums" && track.Kind != "piano" && !isModeledKeys(p, track.Kind) && !(p.Edition == 2 && track.Kind == "audio") && samplers[track.Kind].Name == "" && !isKit && instruments[track.Kind] == nil {
			return fmt.Errorf("track %s has unknown instrument %s", track.ID, track.Kind)
		}
		if (p.Edition == 2 && track.Kind == "audio" || samplers[track.Kind].Name != "") && len(track.Params) > 0 {
			return fmt.Errorf("audio and sampler tracks accept mixer settings only")
		}
		if track.Params == nil {
			return fmt.Errorf("track %s params must be explicit", track.ID)
		}
		if track.Kind == "acid" {
			if _, err := acidParamsFromValues(track.Params); err != nil {
				return fmt.Errorf("track %s: %w", track.ID, err)
			}
		}
		if track.Kind == "drums" {
			if _, err := drumParamsFromValues(track.Params); err != nil {
				return fmt.Errorf("track %s: %w", track.ID, err)
			}
		}
		if isModeledKeys(p, track.Kind) {
			if _, err := KeysSpecFromValues(track.Kind, track.Params); err != nil {
				return fmt.Errorf("track %s: %w", track.ID, err)
			}
		}
		if isModeledPiano(p, track.Kind) {
			if _, err := PianoSustainFromValues(track.Params); err != nil {
				return fmt.Errorf("track %s: %w", track.ID, err)
			}
		}
		if isKit && len(track.Params) != 0 {
			return fmt.Errorf("kit track %s does not accept drum or instrument parameters", track.ID)
		}
		for name, value := range track.Params {
			if name == "level" || name == "pan" {
				return fmt.Errorf("track %s must store %s in mixer", track.ID, name)
			}
			if !validID(name) || !validValue(value) {
				return fmt.Errorf("track %s has invalid parameter %s", track.ID, name)
			}
		}
		if program := instruments[track.Kind]; program != nil {
			overrides := make(map[string]string, len(track.Params))
			for name, value := range track.Params {
				if name == "octave" && !program.HasParameter("octave") {
					if err := validateOctaveValue(value); err != nil {
						return fmt.Errorf("track %s: %w", track.ID, err)
					}
					continue
				}
				literal, err := valueSource(value)
				if err != nil {
					return fmt.Errorf("track %s parameter %s: %w", track.ID, name, err)
				}
				overrides[name] = literal
			}
			lowered, err := instrument.Lower(program, overrides)
			if err != nil {
				return fmt.Errorf("track %s: %w", track.ID, err)
			}
			trackGraphs[track.ID] = lowered
		}
		if p.Format == FormatID && track.Mixer.Solo {
			return fmt.Errorf("project/1 does not support solo")
		}
		if err := validateMixer(track.Mixer); err != nil {
			return fmt.Errorf("track %s: %w", track.ID, err)
		}
		if err := validateMixerLevel(track.Mixer); err != nil {
			return fmt.Errorf("track %s: %w", track.ID, err)
		}
		inserts := track.Mixer.Inserts
		if inserts == nil && track.Mixer.Insert != "none" {
			inserts = []string{track.Mixer.Insert}
		}
		if len(inserts) > 1 {
			return fmt.Errorf("CICADA-UNSUPPORTED: track insert chain is not implemented")
		}
		if len(inserts) == 1 {
			effect, exists := effects[inserts[0]]
			if !exists {
				return fmt.Errorf("track %s references undeclared insert %s", track.ID, inserts[0])
			}
			if semanticEffectKind(effect) != "drive" {
				return fmt.Errorf("CICADA-UNSUPPORTED: %s as a track insert is not implemented", semanticEffectKind(effect))
			}
		}
		if track.Mixer.SendA > 0 && !hasEffectKind(effects, "delay") {
			return fmt.Errorf("track %s requires a declared delay for send_a", track.ID)
		}
		if track.Mixer.SendB > 0 && !hasEffectKind(effects, "reverb") {
			return fmt.Errorf("track %s requires a declared reverb for send_b", track.ID)
		}
		if p.Format == FormatID2 {
			seenSends := map[string]bool{}
			for _, send := range track.Mixer.Sends {
				effect, exists := effects[send.To]
				if !exists {
					return fmt.Errorf("track %s send references unknown effect %s", track.ID, send.To)
				}
				kind := semanticEffectKind(effect)
				if kind != "delay" && kind != "reverb" {
					return fmt.Errorf("CICADA-UNSUPPORTED: send to %s effect is not implemented", kind)
				}
				if seenSends[kind] {
					return fmt.Errorf("track %s has multiple sends to %s", track.ID, kind)
				}
				seenSends[kind] = true
				if !validValue(send.Level) || send.Level.Number == nil || send.Tap != "" && send.Tap != "pre" && send.Tap != "post" {
					return fmt.Errorf("track %s has invalid send", track.ID)
				}
				unit, value := send.Level.Unit, *send.Level.Number
				if unit == "ratio" || unit == "unit" {
					if value < 0 || value > 1 {
						return fmt.Errorf("track %s linear send is outside 0..1", track.ID)
					}
				} else if unit != "db" || value < -60 || value > 0 {
					return fmt.Errorf("CICADA-UNSUPPORTED: track %s send level is outside the engine range", track.ID)
				}
			}
		}
	}
	namespace := map[string]string{}
	for id := range tracks {
		namespace[id] = "track"
	}
	for id := range effects {
		if previous := namespace[id]; previous != "" {
			return fmt.Errorf("duplicate mixer namespace ID %s (%s and effect)", id, previous)
		}
		namespace[id] = "effect"
	}
	seenBuses := map[string]bool{}
	for _, bus := range p.Buses {
		if !validID(bus.ID) || seenBuses[bus.ID] || namespace[bus.ID] != "" {
			return fmt.Errorf("duplicate or invalid bus ID %q", bus.ID)
		}
		seenBuses[bus.ID] = true
		namespace[bus.ID] = "bus"
	}
	if p.Format == FormatID2 {
		for _, bus := range p.Buses {
			if bus.ID != "music" && bus.ID != "sfx" {
				return fmt.Errorf("CICADA-UNSUPPORTED: user-declared bus %s is not implemented", bus.ID)
			}
			if err := validateMixer(bus.Mixer); err != nil {
				return fmt.Errorf("bus %s: %w", bus.ID, err)
			}
			if err := validateMixerLevel(bus.Mixer); err != nil {
				return fmt.Errorf("bus %s: %w", bus.ID, err)
			}
			if len(bus.Mixer.Sends) != 0 || bus.Mixer.SendA != 0 || bus.Mixer.SendB != 0 {
				return fmt.Errorf("CICADA-UNSUPPORTED: sends from bus %s are not implemented", bus.ID)
			}
			if bus.Mixer.Out != nil && *bus.Mixer.Out != bus.ID {
				return fmt.Errorf("CICADA-UNSUPPORTED: bus %s output routing is not implemented", bus.ID)
			}
			if bus.Mixer.Pan != 0 || bus.Mixer.panSet {
				return fmt.Errorf("CICADA-UNSUPPORTED: bus %s pan is not implemented", bus.ID)
			}
			if bus.Mixer.Level != nil {
				off := bus.Mixer.Level.Unit == "enum" && bus.Mixer.Level.Text == "off"
				if !off && (bus.ID != "music" || bus.Mixer.Level.Unit != "db" || bus.Mixer.Level.Number == nil || *bus.Mixer.Level.Number != -3) {
					return fmt.Errorf("CICADA-UNSUPPORTED: bus %s level is not implemented", bus.ID)
				}
			}
			if bus.Mixer.GainDB != -3 && bus.ID == "music" || bus.Mixer.GainDB != 0 && bus.ID == "sfx" {
				return fmt.Errorf("CICADA-UNSUPPORTED: bus %s level is not implemented", bus.ID)
			}
			inserts := bus.Mixer.Inserts
			if inserts == nil && bus.Mixer.Insert != "none" {
				inserts = []string{bus.Mixer.Insert}
			}
			if len(inserts) > 1 {
				return fmt.Errorf("CICADA-UNSUPPORTED: bus %s insert chain is not implemented", bus.ID)
			}
			if len(inserts) == 1 {
				effect, exists := effects[inserts[0]]
				if bus.ID != "music" || !exists || semanticEffectKind(effect) != "comp" {
					return fmt.Errorf("CICADA-UNSUPPORTED: bus music insert must be a compressor")
				}
			}
		}
		if p.Master != nil {
			if len(p.Master.Mixer.Inserts) > 16 {
				return fmt.Errorf("CICADA-LIMIT: master supports at most 16 inserts")
			}
			seen := map[string]bool{}
			for _, name := range p.Master.Mixer.Inserts {
				effect, ok := effects[name]
				if !ok {
					return fmt.Errorf("CICADA-REFERENCE: unknown master effect %s", name)
				}
				if seen[name] {
					return fmt.Errorf("CICADA-DUPLICATE: master repeats effect %s", name)
				}
				seen[name] = true
				if err := validateMasterEffect(p, effect); err != nil {
					return fmt.Errorf("master effect %s: %w", name, err)
				}
			}
		}
		if p.Master != nil {
			if err := validateMixer(p.Master.Mixer); err != nil {
				return fmt.Errorf("master: %w", err)
			}
			if err := validateMixerLevel(p.Master.Mixer); err != nil {
				return fmt.Errorf("master: %w", err)
			}
			if len(p.Master.Mixer.Sends) > 0 || p.Master.Mixer.SendA != 0 || p.Master.Mixer.SendB != 0 || p.Master.Mixer.Out != nil {
				return fmt.Errorf("CICADA-UNSUPPORTED: master sends and output routing are not implemented")
			}
			if p.Master.Mixer.Pan != 0 || p.Master.Mixer.panSet {
				return fmt.Errorf("CICADA-UNSUPPORTED: master pan is not implemented")
			}
		}
		for _, effect := range p.Effects {
			if semanticEffectKind(effect) != "comp" {
				continue
			}
			placed := MasterHasInsert(p, effect.ID)
			for _, bus := range p.Buses {
				onBus := bus.ID == "music" && len(bus.Mixer.Inserts) == 1 && bus.Mixer.Inserts[0] == effect.ID
				if onBus && MasterHasInsert(p, effect.ID) {
					return fmt.Errorf("CICADA-UNSUPPORTED: compressor %s cannot be shared by music and master", effect.ID)
				}
				placed = placed || onBus
			}
			if !placed {
				return fmt.Errorf("CICADA-UNSUPPORTED: compressor %s must be inserted on bus music or master", effect.ID)
			}
		}
		if err := validateExports(p.Exports); err != nil {
			return err
		}
	}
	if compSidechain != "" && compSidechain != "music" && compSidechain != "sfx" {
		if _, ok := tracks[compSidechain]; !ok {
			return fmt.Errorf("compressor sidechain references unknown track %s", compSidechain)
		}
	}
	allocatedVoices := 0
	for _, track := range p.Tracks {
		if sampler := samplers[track.Kind]; sampler.Name != "" {
			allocatedVoices += sampler.Voices
		} else if isModeledKeys(p, track.Kind) {
			spec, err := KeysSpecFromValues(track.Kind, track.Params)
			if err != nil {
				return err
			}
			allocatedVoices += int(spec.Controls[127])
		} else if isModeledPiano(p, track.Kind) {
			allocatedVoices += piano.MaxVoices
		} else if track.Kind == "drums" {
			allocatedVoices += drumVoiceCount(projectDrumLanes(p, track))
		} else if kit, ok := kits[track.Kind]; ok {
			allocatedVoices += len(kit.Lanes)
		} else if inst := instruments[track.Kind]; inst != nil && inst.Mode == "poly" {
			allocatedVoices += 4
		} else {
			allocatedVoices++
		}
	}
	if allocatedVoices > 32 {
		return fmt.Errorf("project exceeds 32 allocated voices")
	}
	patterns := map[string]Pattern{}
	for _, pattern := range p.Patterns {
		if _, exists := patterns[pattern.ID]; exists || !validID(pattern.ID) {
			return fmt.Errorf("duplicate or invalid pattern ID %q", pattern.ID)
		}
		patterns[pattern.ID] = pattern
		if pattern.Steps < 1 || pattern.Steps > 64 || pattern.SwingPercent100 < 5000 || pattern.SwingPercent100 > 7500 || pattern.GatePercent < 10 || pattern.GatePercent > 100 || pattern.Transpose < -24 || pattern.Transpose > 24 {
			return fmt.Errorf("pattern %s has invalid timing or length", pattern.ID)
		}
		if pattern.Data == nil || pattern.Lanes == nil {
			return fmt.Errorf("pattern %s needs explicit data and lanes", pattern.ID)
		}
		if pattern.Kind == "drums" {
			if pattern.Transpose != 0 {
				return fmt.Errorf("drum pattern %s cannot transpose lanes", pattern.ID)
			}
			if len(pattern.Data) != 0 || len(pattern.Lanes) != len(laneOrder) {
				return fmt.Errorf("drum pattern %s needs all eleven lane arrays", pattern.ID)
			}
			for _, lane := range laneOrder {
				steps, ok := pattern.Lanes[lane]
				if !ok || len(steps) != int(pattern.Steps) {
					return fmt.Errorf("drum pattern %s lane %s has wrong length", pattern.ID, lane)
				}
				for _, step := range steps {
					if step != nil && len(step.Notes) > 0 {
						return fmt.Errorf("drum pattern %s cannot contain chords", pattern.ID)
					}
				}
				if err := validateSteps(steps); err != nil {
					return fmt.Errorf("drum pattern %s lane %s: %w", pattern.ID, lane, err)
				}
			}
		} else if pattern.Kind == "acid" || pattern.Kind == "notes" {
			if len(pattern.Data) != int(pattern.Steps) || len(pattern.Lanes) != 0 {
				return fmt.Errorf("note pattern %s has wrong data length", pattern.ID)
			}
			for i, step := range pattern.Data {
				if step != nil && len(step.Notes) > 0 {
					if pattern.Kind != "notes" {
						return fmt.Errorf("chords require a notes pattern")
					}
					previous := pattern.Data[(i+len(pattern.Data)-1)%len(pattern.Data)]
					if previous != nil && previous.Slide {
						return fmt.Errorf("a slide cannot target a chord")
					}
				}
			}
			if err := validateSteps(pattern.Data); err != nil {
				return fmt.Errorf("note pattern %s: %w", pattern.ID, err)
			}
		} else {
			return fmt.Errorf("pattern %s has unknown kind", pattern.ID)
		}
	}
	for _, track := range p.Tracks {
		seen := map[string]bool{}
		for _, slot := range track.Slots {
			if slot == nil {
				continue
			}
			pattern, ok := patterns[*slot]
			if !ok || seen[*slot] || !compatible(track.Kind, pattern.Kind, kits) {
				return fmt.Errorf("track %s has invalid or duplicate slot %s", track.ID, *slot)
			}
			for _, step := range pattern.Data {
				if step != nil && len(step.Notes) > 0 {
					inst := instruments[track.Kind]
					if !isModeledPiano(p, track.Kind) && !isModeledKeys(p, track.Kind) && (inst == nil || inst.Mode != "poly") || pattern.Kind != "notes" {
						return fmt.Errorf("chords require a voice poly instrument and a notes pattern")
					}
				}
			}
			seen[*slot] = true
			if isModeledPiano(p, track.Kind) || isModeledKeys(p, track.Kind) {
				for _, step := range pattern.Data {
					if step == nil || step.Tie {
						continue
					}
					notes := step.Notes
					if len(notes) == 0 {
						notes = []int{int(step.Note)}
					}
					for _, note := range notes {
						if note+int(pattern.Transpose) < 21 || note+int(pattern.Transpose) > 108 {
							return fmt.Errorf("track %s pattern %s: keyboard notes must be MIDI 21 to 108", track.ID, pattern.ID)
						}
					}
				}
			}
			if program := trackGraphs[track.ID]; program.DelaySamples() > 0 {
				for _, step := range pattern.Data {
					if step == nil || step.Tie {
						continue
					}
					notes := step.Notes
					if len(notes) == 0 {
						notes = []int{int(step.Note)}
					}
					for _, note := range notes {
						if err := validateGraphDelayNote(program, 48_000, note+int(pattern.Transpose)); err != nil {
							return fmt.Errorf("CICADA-PARAM: track %s pattern %s: %w", track.ID, pattern.ID, err)
						}
					}
				}
			}
		}
	}
	scenes := map[string]Scene{}
	for _, scene := range p.Scenes {
		if _, exists := scenes[scene.ID]; exists || !validID(scene.ID) || scene.Bindings == nil {
			return fmt.Errorf("duplicate or invalid scene %q", scene.ID)
		}
		scenes[scene.ID] = scene
		for trackID, patternID := range scene.Bindings {
			track, ok := tracks[trackID]
			if !ok {
				return fmt.Errorf("scene %s references unknown track %s", scene.ID, trackID)
			}
			if patternID == "keep" || patternID == "off" {
				continue
			}
			if p.Edition == 2 && track.Kind == "audio" {
				if _, ok := clips[patternID]; !ok {
					return fmt.Errorf("scene %s references unknown clip %s", scene.ID, patternID)
				}
				continue
			}
			pattern, ok := patterns[patternID]
			if !ok || !compatible(track.Kind, pattern.Kind, kits) || !hasSlot(track, patternID) {
				return fmt.Errorf("scene %s cannot bind %s to %s", scene.ID, trackID, patternID)
			}
		}
		seenSettings := make(map[string]bool, len(scene.Settings))
		for _, setting := range scene.Settings {
			if setting.Path == "" || seenSettings[setting.Path] {
				return fmt.Errorf("scene %s has an empty or duplicate setting path %q", scene.ID, setting.Path)
			}
			seenSettings[setting.Path] = true
			resolved, err := ResolveParameterPath(p, setting.Path)
			if err != nil {
				return err
			}
			if !resolved.Descriptor.Live {
				return fmt.Errorf("CICADA-UNSUPPORTED: scene setting %s is not live", setting.Path)
			}
			if setting.Value.Number != nil && (setting.Value.Unit == "enum" || setting.Value.Text != "") || setting.Value.Number == nil && (setting.Value.Unit != "enum" || setting.Value.Text == "") {
				return fmt.Errorf("scene %s setting %s has an invalid value variant", scene.ID, setting.Path)
			}
			if err := validateParameterValue(resolved.Descriptor, setting.Value.projectValue()); err != nil {
				return fmt.Errorf("scene %s setting %s: %w", scene.ID, setting.Path, err)
			}
			if setting.Value.Number != nil {
				if _, err := sceneSettingFloat32Value(resolved.Descriptor.Min, resolved.Descriptor.Max, *setting.Value.Number); err != nil {
					return fmt.Errorf("scene %s setting %s: %w", scene.ID, setting.Path, err)
				}
			}
			if _, _, err := sceneSettingKernelValue(p, setting, resolved); err != nil {
				return fmt.Errorf("scene %s setting %s: %w", scene.ID, setting.Path, err)
			}
		}
	}
	active := map[string]string{}
	for _, entry := range p.Song {
		if entry.Bars < 1 || entry.Bars > 999 {
			return fmt.Errorf("song entry has invalid bar count")
		}
		scene, ok := scenes[entry.Scene]
		if !ok {
			return fmt.Errorf("song references unknown scene %s", entry.Scene)
		}
		for track, pattern := range scene.Bindings {
			if pattern == "off" {
				delete(active, track)
			} else if pattern != "keep" {
				active[track] = pattern
			}
		}
		voices := 0
		for trackID := range active {
			kind := tracks[trackID].Kind
			if sampler := samplers[kind]; sampler.Name != "" {
				voices += sampler.Voices
			} else if isModeledKeys(p, kind) {
				spec, err := KeysSpecFromValues(kind, tracks[trackID].Params)
				if err != nil {
					return err
				}
				voices += int(spec.Controls[127])
			} else if isModeledPiano(p, kind) {
				voices += piano.MaxVoices
			} else if kind == "drums" {
				voices += drumVoiceCount(projectDrumLanes(p, tracks[trackID]))
			} else if kit, ok := kits[kind]; ok {
				voices += len(kit.Lanes)
			} else if inst := instruments[kind]; inst != nil && inst.Mode == "poly" {
				voices += 4
			} else {
				voices++
			}
		}
		if voices > 32 {
			return fmt.Errorf("scene %s exceeds 32 simultaneous voices", scene.ID)
		}
	}
	return nil
}

func validateMixerLevel(mixer Mixer) error {
	if mixer.Level == nil {
		return nil
	}
	level := mixer.Level
	if level.Unit == "enum" && level.Text == "off" && level.Number == nil {
		if !mixer.Mute {
			return fmt.Errorf("level off must also set mute")
		}
		return nil
	}
	if level.Unit != "db" || level.Number == nil || !finite(*level.Number) || *level.Number < -60 || *level.Number > 6 || level.Text != "" {
		return fmt.Errorf("mixer level must be dB from -60 to +6 or off")
	}
	return nil
}

func validID(id string) bool {
	if strings.Contains(id, ".") {
		if len(id) > 64 {
			return false
		}
		for _, part := range strings.Split(id, ".") {
			if !validID(part) {
				return false
			}
		}
		return true
	}
	if len(id) < 1 || len(id) > 64 || !(id[0] == '_' || id[0] >= 'a' && id[0] <= 'z') {
		return false
	}
	for i := 1; i < len(id); i++ {
		if id[i] != '_' && id[i] != '-' && !(id[i] >= 'a' && id[i] <= 'z') && !(id[i] >= '0' && id[i] <= '9') {
			return false
		}
	}
	return true
}

func uniqueID(id string, seen map[string]bool) error {
	if !validID(id) || seen[id] {
		return fmt.Errorf("duplicate or invalid ID %q", id)
	}
	seen[id] = true
	return nil
}

func validNumericUnit(unit string) bool {
	return unit == "unit" || unit == "ratio" || unit == "hz" || unit == "ms" || unit == "db" || unit == "lu" || unit == "lufs" || unit == "dbtp"
}

func semanticEffectKind(effect Effect) string {
	if effect.Kind != "" {
		return effect.Kind
	}
	return effect.ID
}

func hasEffectKind(effects map[string]Effect, kind string) bool {
	for _, effect := range effects {
		if semanticEffectKind(effect) == kind {
			return true
		}
	}
	return false
}

func validateExports(exports []Export) error {
	seen := map[string]bool{}
	for _, export := range exports {
		if !validID(export.ID) || seen[export.ID] {
			return fmt.Errorf("duplicate or invalid export ID %q", export.ID)
		}
		seen[export.ID] = true
		if export.Rate != nil && *export.Rate != 44100 && *export.Rate != 48000 && *export.Rate != 96000 {
			return fmt.Errorf("CICADA-UNSUPPORTED: export %s rate %dHz is not supported by the renderer", export.ID, *export.Rate)
		}
		if export.Bits != nil && *export.Bits != 16 && *export.Bits != 24 && *export.Bits != 32 {
			return fmt.Errorf("export %s bits must be 16, 24, or 32", export.ID)
		}
		if export.Tail != nil && (!validValue(*export.Tail) || export.Tail.Number == nil || export.Tail.Unit != "ms" || *export.Tail.Number < 0 || *export.Tail.Number > 10000) {
			return fmt.Errorf("CICADA-UNSUPPORTED: export %s tail is outside the renderer's 0 to 10 second range", export.ID)
		}
		if export.Loudness != nil && (!validValue(*export.Loudness) || export.Loudness.Number == nil || export.Loudness.Unit != "lufs" && export.Loudness.Unit != "lu" || *export.Loudness.Number < -70 || *export.Loudness.Number > 0) {
			return fmt.Errorf("export %s loudness must be -70 to 0 LUFS", export.ID)
		}
		if export.TruePeak != nil && (!validValue(*export.TruePeak) || export.TruePeak.Number == nil || export.TruePeak.Unit != "dbtp" || *export.TruePeak.Number < -20 || *export.TruePeak.Number > 0) {
			return fmt.Errorf("export %s true_peak must be -20 to 0 dBTP", export.ID)
		}
		if export.TruePeak != nil && export.Loudness == nil {
			return fmt.Errorf("CICADA-UNSUPPORTED: export %s true_peak without loudness targeting is not implemented", export.ID)
		}
		if export.Normalize != nil && *export.Normalize && export.Loudness != nil {
			return fmt.Errorf("CICADA-UNSUPPORTED: export %s cannot combine loudness targeting and normalize", export.ID)
		}
	}
	return nil
}

func validValue(value Value) bool {
	if value.Number != nil {
		return validNumericUnit(value.Unit) && value.Text == "" && finite(*value.Number)
	}
	return value.Unit == "enum" && value.Text != ""
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validateExpr(expr Expr, depth int) (int, error) {
	if depth > 64 {
		return 0, fmt.Errorf("expression exceeds depth 64")
	}
	if expr.Literal != nil {
		if expr.Name != "" || expr.Op != "" || expr.Args != nil || !finite(*expr.Literal) {
			return 0, fmt.Errorf("invalid expression literal")
		}
		return 1, nil
	}
	if expr.Name != "" {
		if !validID(expr.Name) || expr.Op != "" || expr.Args != nil {
			return 0, fmt.Errorf("invalid expression name")
		}
		return 1, nil
	}
	if expr.Op == "" || expr.Args == nil {
		return 0, fmt.Errorf("invalid expression operation")
	}
	if !validExprArity(expr.Op, len(expr.Args)) {
		return 0, fmt.Errorf("unsupported expression operation %s", expr.Op)
	}
	count := 1
	for _, child := range expr.Args {
		n, err := validateExpr(child, depth+1)
		if err != nil {
			return 0, err
		}
		count += n
	}
	if count > 128 {
		return 0, fmt.Errorf("expression exceeds 128 nodes")
	}
	return count, nil
}

func validExprArity(op string, n int) bool {
	switch op {
	case "+", "-", "*", "/", "period", "env", "lowpass", "highpass", "delay", "neural_amp":
		return n == 2
	case "saw", "square", "sine", "tanh", "exp2":
		return n == 1
	case "noise":
		return n == 0
	case "ladder", "diode", "mix", "clamp":
		return n == 3
	case "comb":
		return n == 4
	}
	return false
}

func validateSteps(steps []*Step) error {
	for index, step := range steps {
		if step == nil {
			continue
		}
		if step.Note > 127 || step.Ratchet < 1 || step.Ratchet > 8 || step.Probability > 100 || step.Velocity > 127 {
			return fmt.Errorf("step %d has invalid note or modifiers", index)
		}
		if step.Notes != nil {
			if len(step.Notes) < 2 || len(step.Notes) > 4 {
				return fmt.Errorf("step %d chord needs 2 to 4 pitches", index)
			}
			chord := seq.ChordStep{Count: uint8(len(step.Notes))}
			for i, note := range step.Notes {
				if note < 0 || note > 127 {
					return fmt.Errorf("step %d chord pitch is out of range", index)
				}
				chord.Notes[i] = uint8(note)
			}
			if err := chord.Validate(seq.Step{Note: step.Note, Gate: true, Tie: step.Tie, Slide: step.Slide, Ratchet: step.Ratchet}); err != nil {
				return fmt.Errorf("step %d: %w", index, err)
			}
		}
		if step.Tie && (step.Note != 0 || step.Ratchet != 1 || step.Probability != 100) {
			return fmt.Errorf("step %d has invalid tie encoding", index)
		}
	}
	return nil
}

func compatible(trackKind, patternKind string, kits map[string]Kit) bool {
	if trackKind == "drums" || kits[trackKind].Lanes != nil {
		return patternKind == "drums"
	}
	if trackKind == "acid" {
		return patternKind == "acid" || patternKind == "notes"
	}
	return patternKind == "notes"
}

func hasSlot(track Track, patternID string) bool {
	for _, slot := range track.Slots {
		if slot != nil && *slot == patternID {
			return true
		}
	}
	return false
}
