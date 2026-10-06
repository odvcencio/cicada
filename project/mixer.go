package project

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

// CompileMixerParams resolves a track's local mixer values. Name resolution
// for effect sends and inserts happens after all declarations are collected.
func CompileMixerParams(track notation.Track) (Mixer, error) {
	mixer := defaultMixer()
	var legacyPre bool
	for _, param := range track.Params {
		switch param.Name {
		case "level":
			if param.Value == "off" {
				mixer.Mute, mixer.muteSet = true, true
				continue
			}
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "db" {
				return mixer, fmt.Errorf("level requires dB or off")
			}
			mixer.GainDB = value
			mixer.Level = &Value{Unit: "db", Number: &value}
		case "pan":
			value, unit, err := parseBaseValue(param.Value)
			if err != nil || unit != "unit" {
				return mixer, fmt.Errorf("pan requires a unitless value")
			}
			mixer.Pan = value
			mixer.panSet = true
		case "insert":
			if param.Value == "none" {
				mixer.Insert, mixer.Inserts = "none", []string{}
				continue
			}
			parts := strings.Fields(param.Value)
			var names []string
			for _, part := range parts {
				if part != "->" {
					names = append(names, part)
				}
			}
			if track.Name != "master" && (len(names) != 1 || strings.Contains(param.Value, "->")) {
				return mixer, fmt.Errorf("insert chain is not implemented")
			}
			if len(names) == 0 {
				return mixer, fmt.Errorf("insert requires an effect name or none")
			}
			mixer.Insert, mixer.Inserts = names[0], names
		case "send_a", "send_b":
			value, err := strconv.ParseFloat(param.Value, 64)
			if err != nil {
				return mixer, fmt.Errorf("%s requires a unitless value", param.Name)
			}
			if param.Name == "send_a" {
				mixer.SendA = value
			} else {
				mixer.SendB = value
			}
		case "send_pre":
			on, ok := sourceSwitch(param.Value)
			if !ok {
				return mixer, fmt.Errorf("send_pre requires a switch")
			}
			legacyPre = on
		case "send":
			value, err := projectValue(param.Value)
			if err != nil {
				return mixer, err
			}
			if value.Number == nil {
				return mixer, fmt.Errorf("send level must be numeric")
			}
			if value.Unit == "unit" {
				value.Unit = "ratio"
			}
			if value.Unit == "ratio" && (*value.Number < 0 || *value.Number > 1) {
				return mixer, fmt.Errorf("send level must be a linear gain from 0 to 1")
			}
			if value.Unit == "db" && (*value.Number < -60 || *value.Number > 0) {
				return mixer, fmt.Errorf("send level must be between -60 and 0 dB")
			}
			if value.Unit != "ratio" && value.Unit != "db" {
				return mixer, fmt.Errorf("send level requires linear gain or dB")
			}
			tap := "post"
			if param.Pre {
				tap = "pre"
			}
			mixer.Sends = append(mixer.Sends, MixerSend{To: param.Target, Level: value, Tap: tap})
		case "out", "bus":
			mixer.Bus = param.Value
			mixer.Out = stringPointer(param.Value)
		case "mute", "solo":
			on, ok := sourceSwitch(param.Value)
			if !ok {
				return mixer, fmt.Errorf("%s requires on or off", param.Name)
			}
			if param.Name == "mute" {
				mixer.Mute, mixer.muteSet = on, true
			} else {
				mixer.Solo, mixer.soloSet = on, true
			}
		}
	}
	if legacyPre {
		mixer.SendPre = true
		for i := range mixer.Sends {
			if mixer.Sends[i].Level.Number != nil && *mixer.Sends[i].Level.Number > 0 {
				mixer.Sends[i].Tap = "pre"
			}
		}
	}
	return mixer, validateMixer(mixer)
}

func validateMixer(mixer Mixer) error {
	if math.IsNaN(mixer.GainDB) || math.IsInf(mixer.GainDB, 0) || mixer.GainDB < -60 || mixer.GainDB > 6 || math.IsNaN(mixer.Pan) || math.IsInf(mixer.Pan, 0) || mixer.Pan < -1 || mixer.Pan > 1 {
		return fmt.Errorf("dry mixer level must be -60..+6 dB and pan -1..1")
	}
	if math.IsNaN(mixer.SendA) || math.IsInf(mixer.SendA, 0) || mixer.SendA < 0 || mixer.SendA > 1 {
		return fmt.Errorf("send_a must be 0 to 1")
	}
	if math.IsNaN(mixer.SendB) || math.IsInf(mixer.SendB, 0) || mixer.SendB < 0 || mixer.SendB > 1 {
		return fmt.Errorf("send_b must be 0 to 1")
	}
	if mixer.Bus != "music" && mixer.Bus != "sfx" {
		return fmt.Errorf("bus must be music or sfx")
	}
	return nil
}

func sourceSwitch(source string) (bool, bool) {
	switch source {
	case "on", "true":
		return true, true
	case "off", "false":
		return false, true
	default:
		return false, false
	}
}

func stringPointer(value string) *string { return &value }

func isMixerSourceParam(name string) bool {
	switch name {
	case "level", "pan", "insert", "send_a", "send_b", "send_pre", "bus", "mute", "solo", "send", "out":
		return true
	default:
		return false
	}
}

// IsMixerSourceParam reports whether a notation parameter belongs to the mixer.
func IsMixerSourceParam(name string) bool { return isMixerSourceParam(name) }

func resolveNamedMixer(mixer *Mixer, source notation.Track, effectKinds map[string]string) {
	if mixer == nil {
		return
	}
	if mixer.Inserts == nil && mixer.Insert != "none" {
		mixer.Inserts = []string{mixer.Insert}
	}
	mixer.Sends = nil
	legacyPre := mixer.SendPre
	for _, param := range source.Params {
		target, value, tap := "", Value{}, "post"
		switch param.Name {
		case "send":
			target = param.Target
			value, _ = projectValue(param.Value)
			if value.Unit == "unit" {
				value.Unit = "ratio"
			}
			if param.Pre || legacyPre {
				tap = "pre"
			}
		case "send_a", "send_b":
			linear, err := strconv.ParseFloat(param.Value, 64)
			if err != nil || linear == 0 {
				continue
			}
			kind := "delay"
			if param.Name == "send_b" {
				kind = "reverb"
			}
			for name, effectKind := range effectKinds {
				if effectKind == kind || effectKind == "" && name == kind {
					target = name
					break
				}
			}
			value = Value{Unit: "ratio", Number: &linear}
			if mixer.SendPre {
				tap = "pre"
			}
		default:
			continue
		}
		if value.Number == nil {
			continue
		}
		mixer.Sends = append(mixer.Sends, MixerSend{To: target, Level: value, Tap: tap})
		kind := effectKinds[target]
		if kind == "" {
			kind = target
		}
		linear := *value.Number
		if value.Unit == "db" {
			linear = math.Pow(10, linear/20)
		}
		switch kind {
		case "delay":
			mixer.SendA = linear
		case "reverb":
			mixer.SendB = linear
		}
	}
}

func compileExport(source notation.Export) (Export, error) {
	out := Export{ID: source.Name}
	for _, param := range source.Params {
		value, err := projectValue(param.Value)
		if err != nil {
			return Export{}, fmt.Errorf("export %s %s: %w", source.Name, param.Name, err)
		}
		switch param.Name {
		case "rate":
			if value.Number == nil || value.Unit != "hz" || math.Trunc(*value.Number) != *value.Number || *value.Number < 8000 || *value.Number > 192000 {
				return Export{}, fmt.Errorf("export rate must be an integer from 8000Hz to 192000Hz")
			}
			rate := int(*value.Number)
			out.Rate = &rate
		case "bits":
			number, err := strconv.Atoi(param.Value)
			if err != nil || number != 16 && number != 24 && number != 32 {
				return Export{}, fmt.Errorf("export bits must be 16, 24, or 32")
			}
			out.Bits = &number
		case "tail":
			if value.Number == nil || value.Unit != "ms" || *value.Number < 0 || *value.Number > 30000 {
				return Export{}, fmt.Errorf("export tail must be 0 to 30s")
			}
			out.Tail = &value
		case "loudness":
			if value.Number == nil || value.Unit != "lufs" && value.Unit != "lu" || *value.Number < -70 || *value.Number > 0 {
				return Export{}, fmt.Errorf("export loudness must be -70 to 0 LUFS")
			}
			out.Loudness = &value
		case "true_peak":
			if value.Number == nil || value.Unit != "dbtp" || *value.Number < -20 || *value.Number > 0 {
				return Export{}, fmt.Errorf("export true_peak must be -20 to 0 dBTP")
			}
			out.TruePeak = &value
		case "normalize":
			on, ok := sourceSwitch(param.Value)
			if !ok {
				return Export{}, fmt.Errorf("export normalize must be on or off")
			}
			out.Normalize = &on
		default:
			return Export{}, fmt.Errorf("unknown export setting %s", param.Name)
		}
	}
	return out, nil
}

func sourceUsesNamedMixer(score *notation.Score) bool {
	if score == nil || len(score.Buses) > 0 || score.HasMaster || len(score.Exports) > 0 {
		return score != nil && (len(score.Buses) > 0 || score.HasMaster || len(score.Exports) > 0)
	}
	for _, effect := range score.Effects {
		if !effect.Legacy {
			return true
		}
	}
	for _, track := range score.Tracks {
		for _, param := range track.Params {
			switch param.Name {
			case "send", "out", "mute", "solo":
				return true
			case "level":
				if param.Value == "off" {
					return true
				}
			case "insert":
				if strings.Contains(param.Value, "->") {
					return true
				}
			}
		}
	}
	return false
}

func routedEffects(score *notation.Score) []notation.Effect {
	// Edition 1 routes returns by effect kind and preserves implicit instances.
	if score.Version == 1 {
		return append([]notation.Effect(nil), score.Effects...)
	}
	routed := map[string]bool{}
	visit := func(params []notation.Param) {
		for _, param := range params {
			switch param.Name {
			case "insert":
				for _, name := range strings.Fields(param.Value) {
					routed[name] = true
				}
			case "send":
				routed[param.Target] = true
			}
		}
	}
	for _, track := range score.Tracks {
		visit(track.Params)
	}
	for _, bus := range score.Buses {
		visit(bus.Params)
	}
	effects := make([]notation.Effect, 0, len(score.Effects))
	for _, effect := range score.Effects {
		if score.Origins[effect.Name].Library == "" || routed[effect.Name] {
			effects = append(effects, effect)
		}
	}
	return effects
}
