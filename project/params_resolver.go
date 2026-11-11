package project

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// ResolvedParam names the registry row and concrete kernel slot for one P1
// path. Track is 0xff for a project-level parameter.
type ResolvedParam struct {
	Path       string
	OwnerKind  string
	Owner      string
	Track      uint8
	ID         kernel.ParamID
	Descriptor paramdefs.Descriptor
}

type PathError struct {
	Code string
	Text string
}

func (e *PathError) Error() string { return e.Text }

// ResolveParameterPath resolves source paths in the shared track/effect
// namespace. Effect paths omit the old fx. prefix; sends name their return.
func ResolveParameterPath(p *Project, path string) (ResolvedParam, error) {
	fail := func(code, message string) (ResolvedParam, error) {
		return ResolvedParam{}, &PathError{Code: code, Text: message}
	}
	if p == nil {
		return fail("CICADA-REFERENCE", "cannot resolve a path without a project")
	}
	parts := strings.Split(path, ".")
	if len(parts) == 1 {
		for _, descriptor := range paramdefs.Registry {
			if descriptor.Scope == "global" && descriptor.Path == path {
				id, ok := kernel.FindParam(descriptor.ID)
				if !ok {
					return fail("CICADA-PARAM", "registry has no kernel address for "+path)
				}
				return ResolvedParam{Path: path, OwnerKind: "global", Owner: path, Track: 0xff, ID: id, Descriptor: descriptor}, nil
			}
		}
		return fail("CICADA-REFERENCE", "unknown parameter path "+path)
	}
	owner := parts[0]
	for _, effect := range p.Effects {
		if strings.HasPrefix(path, effect.ID+".") && len(effect.ID) > len(owner) {
			owner = effect.ID
		}
	}
	parts = append([]string{owner}, strings.Split(strings.TrimPrefix(path, owner+"."), ".")...)
	trackIndex, trackOK := -1, false
	for i, track := range p.Tracks {
		if track.ID == owner {
			trackIndex, trackOK = i, true
			break
		}
	}
	effectOK := false
	effectKind := ""
	for _, effect := range p.Effects {
		if effect.ID == owner {
			if MasterHasInsert(p, owner) {
				return fail("CICADA-UNSUPPORTED", "master effect controls are fixed until the score is recompiled")
			}
			if trackHasInsert(p, owner) {
				// Legacy single drive remains addressable. Serial chain controls
				// rebuild the score so different tracks never share mutable state.
				legacy := semanticEffectKind(effect) == "drive"
				for _, track := range p.Tracks {
					names := mixerInsertIDs(track.Mixer)
					for _, name := range names {
						if name == owner && len(names) != 1 {
							legacy = false
						}
					}
				}
				if !legacy {
					return fail("CICADA-UNSUPPORTED", "track insert controls are saved and recompiled; live automation is unavailable")
				}
			}
			effectOK = true
			effectKind = semanticEffectKind(effect)
			break
		}
	}
	busOK := owner == "music" || owner == "sfx"
	for _, bus := range p.Buses {
		busOK = busOK || bus.ID == owner
	}
	if trackOK && effectOK {
		return fail("CICADA-REFERENCE", "ambiguous path owner "+owner+"; hint: rename the track or effect so this path resolves to one owner")
	}
	var descriptor paramdefs.Descriptor
	found := false
	if trackOK {
		setting := strings.Join(parts[1:], ".")
		kind := parameterVoiceKind(p, p.Tracks[trackIndex].Kind)
		// send.<name> of a declared delay, tape or reverb is its return's
		// send; the declared name wins over the return keywords.
		if target, ok, refusal := paramdefs.SendTarget(setting, projectEffectKind(p)); refusal != "" {
			return fail("CICADA-PARAM", "unknown setting in parameter path "+path+": "+refusal)
		} else if ok {
			setting = target
		}
		for _, candidate := range paramdefs.Registry {
			if candidate.Scope == "track" && candidate.Path == setting && paramdefs.HasVoice(candidate, kind) {
				descriptor, found = candidate, true
				break
			}
		}
		if !found {
			// An authored instrument's own parameter is live through the
			// instrument.p slot of its declaration order.
			for _, inst := range p.Instruments {
				if inst.ID != p.Tracks[trackIndex].Kind {
					continue
				}
				for slot, param := range inst.Params {
					if param.ID != setting {
						continue
					}
					if slot >= paramdefs.InstrumentParamSlots {
						return fail("CICADA-UNSUPPORTED", fmt.Sprintf("parameter %s of instrument %s cannot change while it plays: only the first %d parameters an instrument declares can, and %s is number %d", param.ID, inst.ID, paramdefs.InstrumentParamSlots, param.ID, slot+1))
					}
					descriptor, found = paramdefs.InstrumentParamDescriptor(slot, param.ID, param.Unit, param.Default), true
				}
			}
		}
		if !found {
			return fail("CICADA-PARAM", "unknown setting in parameter path "+path)
		}
		if strings.HasPrefix(descriptor.ID, "amp.") {
			if _, err := trackAmpParams(p, p.Tracks[trackIndex]); err != nil {
				return fail("CICADA-UNSUPPORTED", err.Error())
			}
		}
		if strings.HasPrefix(descriptor.ID, "chorus.") {
			if _, err := trackChorusParams(p, p.Tracks[trackIndex]); err != nil {
				return fail("CICADA-UNSUPPORTED", err.Error())
			}
		}
		if strings.HasPrefix(descriptor.ID, "drum.") {
			laneControl := strings.SplitN(descriptor.Source, "_", 2)
			if len(laneControl) == 2 && (laneControl[1] == "level" || laneControl[1] == "pan") {
				// A sampler lane's level and pan are fixed when the track compiles; a live
				// path would be accepted and then change nothing.
				for _, kit := range p.Kits {
					if kit.ID != p.Tracks[trackIndex].Kind {
						continue
					}
					if _, _, sampler := isSamplerTarget(kit.Lanes[laneControl[0]], p.SamplerPackSet()); sampler {
						return fail("CICADA-UNSUPPORTED", "sampler kit lane level and pan must be set in the track block, not live: "+path)
					}
				}
			}
			if len(laneControl) == 2 && laneControl[1] != "level" && laneControl[1] != "pan" {
				for _, kit := range p.Kits {
					if kit.ID == p.Tracks[trackIndex].Kind && strings.HasPrefix(kit.Lanes[laneControl[0]], "model.") {
						return fail("CICADA-UNSUPPORTED", "modeled kit synthesis controls must be set on the track: "+path)
					}
				}
			}
		}
	} else if effectOK {
		setting := strings.Join(parts[1:], ".")
		for _, candidate := range paramdefs.Registry {
			if candidate.Scope == "global" && strings.TrimPrefix(candidate.Path, effectKind+".") == setting && strings.HasPrefix(candidate.ID, "fx."+effectKind+".") {
				descriptor, found = candidate, true
				break
			}
		}
		if !found {
			return fail("CICADA-PARAM", "unknown setting in parameter path "+path)
		}
	} else if owner == "master" || busOK {
		setting := strings.Join(parts[1:], ".")
		scope := "bus"
		if owner == "master" {
			scope = "master"
			if p.Master == nil {
				return fail("CICADA-UNSUPPORTED", "master parameter paths are reserved for named mixer pieces")
			}
		}
		for _, candidate := range paramdefs.Registry {
			if candidate.Scope == scope && candidate.Path == setting {
				descriptor, found = candidate, true
				break
			}
		}
		if !found {
			return fail("CICADA-PARAM", "unknown setting in parameter path "+path)
		}
	} else {
		return fail("CICADA-REFERENCE", "unknown parameter path owner "+owner)
	}
	id, ok := kernel.FindParam(descriptor.ID)
	if !ok {
		return fail("CICADA-PARAM", "registry has no kernel address for "+path)
	}
	result := ResolvedParam{Path: path, Owner: owner, ID: id, Descriptor: descriptor, Track: 0xff}
	if trackOK {
		result.OwnerKind, result.Track = "track", uint8(trackIndex)
	} else if owner == "master" {
		result.OwnerKind = "master"
	} else if busOK {
		result.OwnerKind = "bus"
	} else {
		result.OwnerKind = "effect"
	}
	return result, nil
}

// projectEffectKind reports the kind of a declared effect of the project.
func projectEffectKind(p *Project) func(string) (string, bool) {
	return func(name string) (string, bool) {
		for _, effect := range p.Effects {
			if effect.ID == name {
				return semanticEffectKind(effect), true
			}
		}
		return "", false
	}
}

func parameterVoiceKind(p *Project, kind string) string {
	switch kind {
	case "guitar":
		return "guitar"
	case "acid":
		return "acid"
	case "drums":
		return "drums"
	}
	for _, kit := range p.Kits {
		if kit.ID == kind {
			return "drums"
		}
	}
	if isModeledPiano(p, kind) {
		return "piano"
	}
	if isModeledKeys(p, kind) {
		return keyboard.FamilyName(kind)
	}
	if _, ok := modal.ParseTrackKind(kind); ok {
		return "modal"
	}
	return "instrument"
}

func parseParameterValue(descriptor paramdefs.Descriptor, source string) (Value, error) {
	if source == "off" && descriptor.Off {
		return Value{Unit: "enum", Text: "off"}, nil
	}
	if descriptor.Curve == "toggle" {
		switch source {
		case "on", "true":
			number := float64(1)
			return Value{Unit: "unit", Number: &number}, nil
		case "off", "false":
			number := float64(0)
			return Value{Unit: "unit", Number: &number}, nil
		}
	}
	value, err := projectValue(source)
	if err != nil {
		return Value{}, err
	}
	if paramdefs.IsTrackSend(descriptor) && value.Unit == "db" && value.Number != nil {
		// A send takes dB as a track block's send line does, with the same
		// conversion; the lane then holds, and interpolates, linear gain.
		db := *value.Number
		if math.IsNaN(db) || db < paramdefs.SendMinDB || db > paramdefs.SendMaxDB {
			return Value{}, fmt.Errorf("send level %s is outside %ddB to %ddB", source, paramdefs.SendMinDB, paramdefs.SendMaxDB)
		}
		gain := paramdefs.SendGainFromDB(db)
		return Value{Unit: "unit", Number: &gain}, nil
	}
	if value.Number == nil {
		for _, candidate := range descriptor.Values {
			if value.Text == candidate {
				return value, nil
			}
		}
		return Value{}, fmt.Errorf("invalid enum value %q", source)
	}
	if err := validateParameterValue(descriptor, value); err != nil {
		return Value{}, err
	}
	return value, nil
}

func validateParameterValue(descriptor paramdefs.Descriptor, value Value) error {
	if value.Unit == "enum" {
		if descriptor.Off && value.Text == "off" {
			return nil
		}
		for _, candidate := range descriptor.Values {
			if value.Text == candidate {
				return nil
			}
		}
		return fmt.Errorf("invalid enum value %q", value.Text)
	}
	if value.Number == nil || math.IsNaN(*value.Number) || math.IsInf(*value.Number, 0) {
		return fmt.Errorf("expected a finite numeric value")
	}
	want := strings.ToLower(descriptor.Unit)
	if want == "" || want == "ratio" || want == "semitone" || want == "cent" || want == "bpm" {
		want = "unit"
	}
	if value.Unit != want {
		return fmt.Errorf("expected %s; got %s", paramdefs.UnitExpectation(descriptor), paramdefs.ValueWithUnit(*value.Number, value.Unit))
	}
	if *value.Number < descriptor.Min || *value.Number > descriptor.Max {
		return fmt.Errorf("value is outside %g..%g", descriptor.Min, descriptor.Max)
	}
	if descriptor.Type == "integer" && *value.Number != math.Trunc(*value.Number) {
		return fmt.Errorf("value must be a whole number")
	}
	if descriptor.Curve == "toggle" && *value.Number != 0 && *value.Number != 1 {
		return fmt.Errorf("toggle value must be on or off")
	}
	return nil
}
