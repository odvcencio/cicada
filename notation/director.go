package notation

import "math"

// ValidateDirector checks references and dedicated stinger tracks in the live block.
func ValidateDirector(live *Live, tracks []Track, patterns []Pattern, scenes []Scene, kits []Kit) []Diagnostic {
	if live == nil {
		return nil
	}
	var ds []Diagnostic
	add := func(message string, pos Position) {
		if pos.Line == 0 {
			pos = Position{Line: 1, Column: 1}
		}
		ds = append(ds, Diagnostic{Code: "CICADA-DIRECTOR", Severity: "error", Message: message, Position: pos})
	}
	name := func(value string, pos Position) {
		if len(value) < 1 || len(value) > 64 {
			add("director name must contain 1 to 64 bytes", pos)
		}
	}
	timing := func(q string, ms float64, pos Position) {
		if q != "beat" && q != "bar" && q != "phrase" {
			add("quantize must be beat, bar, or phrase", pos)
		}
		if q == "phrase" && live.PhraseBars == 0 {
			add("phrase quantization requires a phrase length", pos)
		}
		if math.IsNaN(ms) || math.IsInf(ms, 0) || ms < 0 || ms > 60000 {
			add("crossfade must be a duration from 0 to 60s", pos)
		}
	}
	sceneNames := map[string]bool{}
	for _, scene := range scenes {
		sceneNames[scene.Name] = true
	}
	stateNames := map[string]bool{}
	if len(live.States) > 64 || len(live.Stingers) > 64 || len(live.Transitions) > 256 {
		add("director supports 64 states, 64 stingers, and 256 transitions", live.Position)
	}
	for _, state := range live.States {
		name(state.Name, state.Position)
		if stateNames[state.Name] {
			add("duplicate state "+state.Name, state.Position)
		}
		stateNames[state.Name] = true
		if !sceneNames[state.Scene] {
			add("unknown state scene "+state.Scene, state.Position)
		}
	}
	transitions := map[string]bool{}
	for _, transition := range live.Transitions {
		if !stateNames[transition.From] || !stateNames[transition.To] {
			add("transition references an unknown state", transition.Position)
		}
		key := transition.From + "/" + transition.To
		if transitions[key] {
			add("duplicate transition "+key, transition.Position)
		}
		transitions[key] = true
		timing(transition.Quantize, transition.CrossfadeMS, transition.Position)
	}
	kitNames := map[string]bool{}
	for _, kit := range kits {
		kitNames[kit.Name] = true
	}
	trackNames := map[string]Track{}
	for _, track := range tracks {
		trackNames[track.Name] = track
	}
	patternNames := map[string]Pattern{}
	for _, pattern := range patterns {
		patternNames[pattern.Name] = pattern
	}
	stingers := map[string]bool{}
	for _, stinger := range live.Stingers {
		name(stinger.Name, stinger.Position)
		if stingers[stinger.Name] {
			add("duplicate stinger "+stinger.Name, stinger.Position)
		}
		stingers[stinger.Name] = true
		timing(stinger.Quantize, stinger.CrossfadeMS, stinger.Position)
		track, ok := trackNames[stinger.Track]
		pattern, found := patternNames[stinger.Pattern]
		if !ok {
			add("unknown stinger track "+stinger.Track, stinger.Position)
		}
		if !found {
			add("unknown stinger pattern "+stinger.Pattern, stinger.Position)
		}
		if ok && found && (track.Kind == "audio" || (pattern.Kind == "drums") != (track.Kind == "drums" || kitNames[track.Kind]) || pattern.Kind == "acid" && track.Kind != "acid") {
			add("stinger pattern kind differs from track instrument", stinger.Position)
		}
		for _, scene := range scenes {
			for _, binding := range scene.Bindings {
				if binding.Track == stinger.Track && binding.Pattern != "off" && (binding.Pattern != "stop" || patternNames["stop"].Name != "") {
					add("stinger track must be off or omitted in every scene", stinger.Position)
				}
			}
		}
		for _, layers := range live.Layers {
			for _, rule := range layers.Rules {
				if rule.Track == stinger.Track {
					add("stinger track cannot have a macro layer rule", stinger.Position)
				}
			}
		}
	}
	return ds
}
