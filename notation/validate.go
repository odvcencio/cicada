package notation

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

var acidParams = map[string]bool{
	"tune": true, "fine": true, "wave": true, "pw": true, "detune": true,
	"sub": true, "cutoff": true, "reso": true, "envmod": true, "decay": true,
	"accent": true, "drive": true, "release": true, "slide": true,
	"gate": true, "filter": true, "savage": true, "octave": true,
}

var mixerParams = map[string]bool{
	"level": true, "pan": true, "send_a": true, "send_b": true,
	"send_pre": true, "mute": true, "solo": true, "insert": true, "bus": true,
	"send": true, "out": true,
}

var drumParams = map[string]map[string]bool{
	"bd": {"tune": true, "decay": true, "sweep": true, "sweep_time": true, "click": true, "drive": true},
	"sd": {"tune": true, "tone": true, "mix": true, "snappy": true, "decay": true},
	"ch": {"tune": true, "decay": true, "tone": true, "metal": true},
	"oh": {"tune": true, "decay": true, "tone": true, "metal": true},
	"cp": {"tune": true, "tone": true, "decay": true, "spread": true},
	"rs": {"tune": true, "decay": true},
	"lt": {"tune": true, "decay": true, "sweep": true},
	"mt": {"tune": true, "decay": true, "sweep": true},
	"ht": {"tune": true, "decay": true, "sweep": true},
	"cb": {"tune": true, "decay": true},
	"cy": {"tune": true, "decay": true, "tone": true},
}

var scales = map[string]bool{
	"minor": true, "major": true, "dorian": true, "phrygian": true,
	"harmonic": true, "pent": true, "mixo": true, "blues": true,
}

// Validate checks the meaning of a syntactically valid score. It leaves the
// source model unchanged, including any invalid slide flags, for editor use.
func Validate(s *Score) []Diagnostic {
	ds := ValidateAudio(s)
	ds = append(ds, ValidateArrangement(s)...)
	ds = append(ds, ValidateLive(s.Live, s.Tracks)...)
	add := func(code, message, severity string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Message: message, Severity: severity, Position: p})
	}
	checkID := func(id string, p Position) {
		if len(id) < 1 || len(id) > 64 {
			add("CICADA-LIMIT", "identifier must contain 1 to 64 bytes", "error", p)
		}
	}
	if s.Live != nil && s.Version != 2 {
		add("CICADA-VERSION", "live controls require edition 2", "error", s.Live.Position)
	}
	if s.Version != 1 && s.Version != 2 {
		add("CICADA-VERSION", "only cicada 1 and 2 are supported", "error", Position{1, 1})
	}
	if s.Version == 2 {
		for _, effect := range s.Effects {
			if effect.Legacy {
				add("CICADA-VERSION", "legacy fx shorthand requires an explicit effect kind in edition 2", "error", effect.Position)
			}
		}
		for _, track := range s.Tracks {
			for _, param := range track.Params {
				if param.Name == "send_a" || param.Name == "send_b" || param.Name == "send_pre" || param.Name == "bus" || param.Name == "level" && param.Value == "off" {
					add("CICADA-VERSION", "legacy mixer spelling "+param.Name+" requires cicada fix in edition 2", "error", param.Position)
				}
			}
		}
	}
	if s.TempoMilli < 20_000 || s.TempoMilli > 300_000 {
		add("CICADA-PARAM", "tempo must be 20 to 300 BPM with at most three decimals", "error", Position{1, 1})
	}
	if !utf8.ValidString(s.Title) || utf8.RuneCountInString(s.Title) > 120 {
		add("CICADA-LIMIT", "title must contain at most 120 Unicode characters", "error", s.TitlePosition)
	}
	if !validKeyRoot(s.KeyRoot) || !scales[s.Scale] {
		add("CICADA-KEY", "unknown key or scale", "error", Position{1, 1})
	}
	if s.SeedLiteral != "" {
		if _, err := strconv.ParseUint(s.SeedLiteral, 10, 32); err != nil {
			add("CICADA-SEED", "seed must be 0 to 4294967295", "error", s.SeedPosition)
		}
	}
	if len(s.Tracks) == 0 || len(s.Tracks) > 16 {
		add("CICADA-LIMIT", "score must have 1 to 16 tracks", "error", Position{1, 1})
	}
	instruments := make(map[string]Instrument, len(s.Instruments))
	for _, inst := range s.Instruments {
		checkID(inst.Name, inst.Position)
		if inst.Name == "acid" || inst.Name == "drums" || inst.Name == "audio" {
			add("CICADA-DUPLICATE", "instrument name is reserved: "+inst.Name, "error", inst.Position)
		}
		if _, exists := instruments[inst.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate instrument "+inst.Name, "error", inst.Position)
		}
		instruments[inst.Name] = inst
		if inst.Octave < 0 || inst.Octave > 6 {
			add("CICADA-PARAM", "instrument octave must be 0 to 6", "error", inst.OctavePosition)
		}
		if inst.Mode != "mono" && inst.Mode != "poly" {
			add("CICADA-PARAM", "voice mode must be mono or poly", "error", inst.Position)
		} else if inst.Mode == "poly" {
			add("CICADA-UNSUPPORTED", "poly voices are not implemented", "error", inst.Position)
		}
		if inst.Output == nil {
			add("CICADA-PARAM", "voice needs an out expression", "error", inst.Position)
		}
		seen := map[string]bool{}
		for _, param := range inst.Params {
			checkID(param.Name, param.Position)
			if seen[param.Name] {
				add("CICADA-DUPLICATE", "duplicate instrument parameter "+param.Name, "error", param.Position)
			}
			seen[param.Name] = true
			if param.Unit != "" && param.Unit != "hz" && param.Unit != "ms" && param.Unit != "unit" && param.Unit != "db" {
				add("CICADA-UNIT", "instrument parameter unit must be hz, ms, unit, or db", "error", param.Position)
			}
		}
		for _, let := range inst.Lets {
			checkID(let.Name, let.Position)
		}
	}
	kits := make(map[string]Kit, len(s.Kits))
	for _, kit := range s.Kits {
		checkID(kit.Name, kit.Position)
		_, instrumentNameTaken := instruments[kit.Name]
		if kit.Name == "acid" || kit.Name == "drums" || kit.Name == "audio" || instrumentNameTaken {
			add("CICADA-DUPLICATE", "kit name is reserved or already declared: "+kit.Name, "error", kit.Position)
		}
		if _, exists := kits[kit.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate kit "+kit.Name, "error", kit.Position)
		}
		kits[kit.Name] = kit
		seenLanes := map[string]bool{}
		for _, binding := range kit.Bindings {
			if drumParams[binding.Lane] == nil {
				add("CICADA-REFERENCE", "unknown kit lane "+binding.Lane, "error", binding.Position)
			}
			if seenLanes[binding.Lane] {
				add("CICADA-DUPLICATE", "kit binds lane twice: "+binding.Lane, "error", binding.Position)
			}
			seenLanes[binding.Lane] = true
			if strings.HasPrefix(binding.Target, "builtin.") {
				lane := strings.TrimPrefix(binding.Target, "builtin.")
				if drumParams[lane] == nil {
					add("CICADA-REFERENCE", "unknown built-in drum "+lane, "error", binding.Position)
				}
			} else if _, exists := instruments[binding.Target]; !exists {
				add("CICADA-REFERENCE", "unknown kit instrument "+binding.Target, "error", binding.Position)
			}
		}
	}
	trackByName := make(map[string]Track, len(s.Tracks))
	namespace := map[string]string{"music": "built-in bus", "sfx": "built-in bus"}
	for _, t := range s.Tracks {
		checkID(t.Name, t.Position)
		checkID(t.Kind, t.Position)
		if _, exists := trackByName[t.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate track "+t.Name, "error", t.Position)
		}
		if previous, exists := namespace[t.Name]; exists {
			add("CICADA-DUPLICATE", "track name "+t.Name+" conflicts with "+previous, "error", t.Position)
		}
		trackByName[t.Name] = t
		namespace[t.Name] = "track"
		if t.Kind == "audio" && s.Version != 2 {
			add("CICADA-VERSION", "expected edition 2 for audio; actual 1", "error", t.Position)
		}
		if t.Kind != "acid" && t.Kind != "drums" && t.Kind != "audio" && !scoreHasSampler(s, t.Kind) {
			if _, instrumentOK := instruments[t.Kind]; !instrumentOK {
				if _, kitOK := kits[t.Kind]; !kitOK {
					add("CICADA-REFERENCE", "unknown instrument "+t.Kind, "error", t.Position)
				}
			}
		}
		seen := map[string]bool{}
		for _, param := range t.Params {
			checkID(param.Name, param.Position)
			key := param.Name
			if param.Name == "send" {
				key += "." + param.Target
			}
			if seen[key] {
				add("CICADA-DUPLICATE", "duplicate parameter "+key, "error", param.Position)
			}
			seen[key] = true
			if !validTrackParam(t.Kind, param.Name, instruments) {
				add("CICADA-PARAM", "unknown parameter "+param.Name, "error", param.Position)
			}
		}
	}
	for _, phrase := range s.Phrases {
		checkID(phrase.Name, phrase.Position)
	}
	if len(s.Patterns) == 0 && len(s.Clips) == 0 {
		add("CICADA-LIMIT", "score needs at least one pattern", "error", Position{1, 1})
	}
	patterns := make(map[string]Pattern, len(s.Patterns))
	explicitSlots := make(map[string]struct {
		index    int
		position Position
	})
	for _, p := range s.Patterns {
		checkID(p.Name, p.Position)
		if _, exists := patterns[p.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate pattern "+p.Name, "error", p.Position)
		}
		patterns[p.Name] = p
		steps := 0
		if (p.Kind == "acid" || p.Kind == "notes") && len(p.Steps) > 0 {
			steps = len(p.Steps)
		} else if len(p.Lanes) > 0 {
			steps = len(p.Lanes[0].Hits)
		}
		if steps < 1 || steps > 64 {
			add("CICADA-LIMIT", "pattern must have 1 to 64 steps", "error", p.Position)
		}
		seenAttrs := map[string]bool{}
		for _, a := range p.Attrs {
			if seenAttrs[a.Name] {
				add("CICADA-DUPLICATE", "duplicate pattern attribute "+a.Name, "error", a.Position)
			}
			seenAttrs[a.Name] = true
			switch a.Name {
			case "steps":
				n, err := strconv.Atoi(a.Value)
				if err != nil || n != steps {
					add("CICADA-PARAM", "steps attribute must equal the number of cells", "error", a.Position)
				}
			case "swing":
				n := parseMilli(strings.TrimSuffix(a.Value, "%"))
				if n < 50_000 || n > 75_000 {
					add("CICADA-PARAM", "swing must be 50 to 75 percent", "error", a.Position)
				}
			case "gate":
				n, err := strconv.Atoi(strings.TrimSuffix(a.Value, "%"))
				if err != nil || n < 10 || n > 100 {
					add("CICADA-PARAM", "gate must be 10 to 100 percent", "error", a.Position)
				}
			case "seed":
				if _, err := strconv.ParseUint(a.Value, 10, 32); err != nil {
					add("CICADA-SEED", "pattern seed must be 0 to 4294967295", "error", a.ValuePosition)
				}
			case "transpose":
				n, err := strconv.Atoi(a.Value)
				if err != nil || n < -24 || n > 24 {
					add("CICADA-PARAM", "transpose must be -24 to 24 semitones", "error", a.ValuePosition)
				} else if p.Kind == "drums" && n != 0 {
					add("CICADA-UNSUPPORTED", "drum patterns cannot transpose lanes", "error", a.ValuePosition)
				}
			case "slot":
				n, err := strconv.Atoi(a.Value)
				if err != nil || n < 0 || n > 15 {
					add("CICADA-PARAM", "slot must be an integer from 0 to 15", "error", a.ValuePosition)
				} else {
					explicitSlots[p.Name] = struct {
						index    int
						position Position
					}{n, a.ValuePosition}
				}
			default:
				add("CICADA-PARAM", "unknown pattern attribute "+a.Name, "error", a.Position)
			}
		}
		if p.Kind == "acid" || p.Kind == "notes" {
			for i, token := range p.Steps {
				if (s.Scale == "pent" || s.Scale == "blues") && len(token.Text) > 0 && (token.Text[0] == '2' || token.Text[0] == '6') {
					add("CICADA-SCALE-DEGREE", "scale "+s.Scale+" has no degree "+token.Text[:1], "error", token.Position)
				}
				if strings.Contains(token.Text, "~") && p.Steps[(i+1)%len(p.Steps)].Text == "." {
					message := "slide has no target in this pattern; a pattern or scene switch may supply one"
					if i+1 < len(p.Steps) {
						message = "slide into the next rest has no effect unless the pattern switches"
					}
					add("CICADA-SLIDE-REST", message, "warning", token.Position)
				}
			}
		} else {
			seenLanes := map[string]bool{}
			for _, lane := range p.Lanes {
				if _, ok := drumParams[lane.Name]; !ok {
					add("CICADA-REFERENCE", "unknown drum lane "+lane.Name, "error", lane.Position)
				}
				if seenLanes[lane.Name] {
					add("CICADA-DUPLICATE", "duplicate drum lane "+lane.Name, "error", lane.Position)
				}
				seenLanes[lane.Name] = true
				if len(lane.Hits) != steps {
					add("CICADA-PARAM", "drum lane length differs from pattern", "error", lane.Position)
				}
			}
		}
	}
	scenes := make(map[string]bool, len(s.Scenes))
	for _, scene := range s.Scenes {
		checkID(scene.Name, scene.Position)
		if scenes[scene.Name] {
			add("CICADA-DUPLICATE", "duplicate scene "+scene.Name, "error", scene.Position)
		}
		scenes[scene.Name] = true
		seenTracks := map[string]bool{}
		for _, b := range scene.Bindings {
			checkID(b.Track, b.Position)
			checkID(b.Pattern, b.Position)
			track, trackOK := trackByName[b.Track]
			if !trackOK {
				add("CICADA-REFERENCE", "scene references unknown track "+b.Track, "error", b.Position)
			}
			if seenTracks[b.Track] {
				add("CICADA-DUPLICATE", "scene assigns track twice: "+b.Track, "error", b.Position)
			}
			seenTracks[b.Track] = true
			if b.Pattern == "keep" || b.Pattern == "off" || b.Pattern == "stop" && patterns["stop"].Name == "" {
				continue
			}
			if track.Kind == "audio" {
				names := []string{}
				for _, clip := range s.Clips {
					names = append(names, clip.Name)
				}
				if !scoreHasClip(s, b.Pattern) {
					add("CICADA-REFERENCE", AudioReferenceMessage("clip", b.Pattern, names), "error", b.Position)
				}
				continue
			}
			if scoreHasClip(s, b.Pattern) {
				add("CICADA-CLIP-RANGE", "expected audio track for clip; actual "+track.Kind, "error", b.Position)
				continue
			}
			pattern, patternOK := patterns[b.Pattern]
			if !patternOK {
				add("CICADA-REFERENCE", "scene references unknown pattern "+b.Pattern, "error", b.Position)
			} else if trackOK {
				_, kitTrack := kits[track.Kind]
				compatible := ((track.Kind == "drums" || kitTrack) && pattern.Kind == "drums") ||
					(track.Kind == "acid" && (pattern.Kind == "acid" || pattern.Kind == "notes")) ||
					(track.Kind != "acid" && track.Kind != "drums" && !kitTrack && pattern.Kind == "notes")
				if !compatible {
					add("CICADA-PARAM", "pattern kind differs from track instrument", "error", b.Position)
				}
			}
		}
	}
	usedPatterns := make(map[string]map[string]bool)
	for _, scene := range s.Scenes {
		for _, binding := range scene.Bindings {
			if trackByName[binding.Track].Kind == "audio" || binding.Pattern == "off" || binding.Pattern == "keep" || binding.Pattern == "stop" && patterns["stop"].Name == "" {
				continue
			}
			if usedPatterns[binding.Track] == nil {
				usedPatterns[binding.Track] = make(map[string]bool)
			}
			usedPatterns[binding.Track][binding.Pattern] = true
		}
	}
	for _, track := range s.Tracks {
		used := usedPatterns[track.Name]
		if len(used) > 16 {
			add("CICADA-LIMIT", "track "+track.Name+" uses more than 16 patterns", "error", track.Position)
		}
		slots := make(map[int]string)
		for _, pattern := range s.Patterns {
			if !used[pattern.Name] {
				continue
			}
			if slot, ok := explicitSlots[pattern.Name]; ok {
				if previous, taken := slots[slot.index]; taken && previous != pattern.Name {
					add("CICADA-DUPLICATE", "track "+track.Name+" slot is assigned to both "+previous+" and "+pattern.Name, "error", slot.position)
				}
				slots[slot.index] = pattern.Name
			}
		}
	}
	for _, entry := range s.Song {
		checkID(entry.Scene, entry.Position)
		if !scenes[entry.Scene] {
			add("CICADA-REFERENCE", "song references unknown scene "+entry.Scene, "error", entry.Position)
		}
		if entry.Bars < 1 || entry.Bars > 999 {
			add("CICADA-LIMIT", "song entry must be 1 to 999 bars", "error", entry.Position)
		}
	}
	if len(s.Song) == 0 && s.Arrange == nil {
		position := s.SongPosition
		if position.Line == 0 {
			position = Position{1, 1}
		}
		add("CICADA-LIMIT", "song must contain at least one scene entry", "error", position)
	}
	declaredEffects := map[string]Effect{}
	kindCounts := map[string]int{}
	for _, effect := range s.Effects {
		checkID(effect.Name, effect.Position)
		if previous, exists := namespace[effect.Name]; exists {
			add("CICADA-DUPLICATE", "effect name "+effect.Name+" conflicts with "+previous, "error", effect.Position)
		}
		if _, exists := declaredEffects[effect.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate effect "+effect.Name, "error", effect.Position)
		}
		namespace[effect.Name] = "effect"
		declaredEffects[effect.Name] = effect
		kindCounts[effect.Kind]++
		if effect.Kind != "drive" && effect.Kind != "delay" && effect.Kind != "reverb" && effect.Kind != "comp" {
			add("CICADA-UNSUPPORTED", "effect kind "+effect.Kind+" is not implemented", "error", effect.Position)
		}
		seen := map[string]bool{}
		for _, param := range effect.Params {
			if seen[param.Name] {
				add("CICADA-DUPLICATE", "duplicate effect parameter "+param.Name, "error", param.Position)
			}
			seen[param.Name] = true
			if effect.Kind == "comp" && param.Name == "sidechain" {
				reference := param.Value
				if strings.HasPrefix(reference, "\"") {
					if decoded, err := strconv.Unquote(reference); err == nil {
						reference = decoded
					}
				}
				if reference != "music" && reference != "sfx" {
					if _, ok := trackByName[reference]; !ok {
						add("CICADA-REFERENCE", "compressor sidechain references unknown track "+reference, "error", param.ValuePosition)
					}
				}
			}
		}
	}
	for _, kind := range []string{"delay", "reverb", "comp"} {
		if kindCounts[kind] > 1 {
			add("CICADA-UNSUPPORTED", "multiple "+kind+" instances are not implemented", "error", Position{1, 1})
		}
	}
	for _, bus := range s.Buses {
		checkID(bus.Name, bus.Position)
		if previous, exists := namespace[bus.Name]; exists && previous != "built-in bus" {
			add("CICADA-DUPLICATE", "bus name "+bus.Name+" conflicts with "+previous, "error", bus.Position)
		}
		namespace[bus.Name] = "bus"
		if bus.Name != "music" && bus.Name != "sfx" {
			add("CICADA-UNSUPPORTED", "user-declared bus "+bus.Name+" is not implemented", "error", bus.Position)
			continue
		}
		seen := map[string]bool{}
		for _, param := range bus.Params {
			key := param.Name
			if param.Name == "send" {
				key += "." + param.Target
			}
			if seen[key] {
				add("CICADA-DUPLICATE", "duplicate music bus setting "+key, "error", param.Position)
			}
			seen[key] = true
			switch param.Name {
			case "insert":
				if param.Value == "none" {
					continue
				}
				chain := strings.Fields(param.Value)
				var names []string
				for _, part := range chain {
					if part != "->" {
						names = append(names, part)
					}
				}
				if bus.Name != "music" || len(names) != 1 || strings.Contains(param.Value, "->") {
					add("CICADA-UNSUPPORTED", bus.Name+" bus insert "+param.Value+" is not implemented", "error", param.ValuePosition)
					continue
				}
				effect, ok := declaredEffects[names[0]]
				if !ok {
					add("CICADA-REFERENCE", "music bus insert references undeclared effect "+names[0], "error", param.ValuePosition)
				} else if effect.Kind != "comp" {
					add("CICADA-UNSUPPORTED", "music bus insert of "+effect.Kind+" is not implemented", "error", param.ValuePosition)
				}
			case "level":
				if param.Value == "off" {
					continue
				}
				value, unit, err := notationBaseValue(param.Value)
				if bus.Name != "music" || err != nil || unit != "db" || value != -3 {
					add("CICADA-UNSUPPORTED", bus.Name+" bus level "+param.Value+" is not implemented", "error", param.ValuePosition)
				}
			case "mute", "solo":
				on, ok := mixerSwitch(param.Value)
				if !ok {
					add("CICADA-PARAM", param.Name+" must be on or off", "error", param.ValuePosition)
				} else if param.Name == "solo" && on {
					add("CICADA-SOLO", "solo is enabled for bus "+bus.Name, "warning", param.ValuePosition)
				}
			case "send", "out", "pan":
				add("CICADA-UNSUPPORTED", bus.Name+" bus "+param.Name+" is not implemented", "error", param.Position)
			default:
				add("CICADA-PARAM", "unknown bus mixer setting "+param.Name, "error", param.Position)
			}
		}
	}
	for _, param := range s.Master {
		switch param.Name {
		case "insert":
			if param.Value != "none" {
				add("CICADA-UNSUPPORTED", "master inserts are not implemented", "error", param.ValuePosition)
			}
		case "level":
			if param.Value != "off" {
				value, unit, err := notationBaseValue(param.Value)
				if err != nil || unit != "db" || value < -60 || value > 6 {
					add("CICADA-PARAM", "master level must be -60 to +6 dB or off", "error", param.ValuePosition)
				}
			}
		case "mute", "solo":
			on, ok := mixerSwitch(param.Value)
			if !ok {
				add("CICADA-PARAM", param.Name+" must be on or off", "error", param.ValuePosition)
			} else if param.Name == "solo" && on {
				add("CICADA-SOLO", "solo is enabled for master", "warning", param.ValuePosition)
			}
		case "pan":
			add("CICADA-UNSUPPORTED", "master pan is not implemented", "error", param.Position)
		default:
			add("CICADA-UNSUPPORTED", "master "+param.Name+" is not implemented", "error", param.Position)
		}
	}
	seenExports := map[string]bool{}
	for _, export := range s.Exports {
		checkID(export.Name, export.Position)
		if seenExports[export.Name] {
			add("CICADA-DUPLICATE", "duplicate export "+export.Name, "error", export.Position)
		}
		seenExports[export.Name] = true
		seen := map[string]bool{}
		for _, param := range export.Params {
			if seen[param.Name] {
				add("CICADA-DUPLICATE", "duplicate export setting "+param.Name, "error", param.Position)
			}
			seen[param.Name] = true
			switch param.Name {
			case "rate", "bits", "tail", "loudness", "true_peak", "normalize":
			default:
				add("CICADA-PARAM", "unknown export setting "+param.Name, "error", param.Position)
			}
		}
	}
	for _, track := range s.Tracks {
		validateTrackMixer(s, track, declaredEffects, namespace, add)
	}
	validateSceneSettings(s, add)
	return ds
}

func validTrackParam(kind, name string, instruments map[string]Instrument) bool {
	if mixerParams[name] {
		return true
	}
	if kind == "acid" {
		return acidParams[name]
	}
	if kind == "drums" {
		parts := strings.SplitN(name, "_", 2)
		if len(parts) == 2 && drumParams[parts[0]] != nil && (parts[1] == "level" || parts[1] == "pan") {
			return true
		}
		return len(parts) == 2 && drumParams[parts[0]][parts[1]]
	}
	if inst, ok := instruments[kind]; ok {
		if name == "octave" {
			return true
		}
		for _, param := range inst.Params {
			if param.Name == name {
				return true
			}
		}
	}
	return false
}

func validateTrackMixer(score *Score, track Track, effects map[string]Effect, namespace map[string]string, add func(string, string, string, Position)) {
	seen := map[string]bool{}
	insertSeen := false
	legacyPre := false
	for _, param := range track.Params {
		key := param.Name
		if param.Name == "send" {
			key += "." + param.Target
		}
		if seen[key] {
			add("CICADA-DUPLICATE", "duplicate mixer setting "+key, "error", param.Position)
		}
		seen[key] = true
		switch param.Name {
		case "mute", "solo":
			on, ok := mixerSwitch(param.Value)
			if !ok {
				add("CICADA-PARAM", param.Name+" must be on or off", "error", param.ValuePosition)
			} else if param.Name == "solo" && on {
				add("CICADA-SOLO", "solo is enabled for track "+track.Name, "warning", param.ValuePosition)
			}
		case "insert":
			insertSeen = true
			if param.Value == "none" {
				continue
			}
			chain := strings.Fields(param.Value)
			var names []string
			for i := 0; i < len(chain); i++ {
				if chain[i] != "->" {
					names = append(names, chain[i])
				}
			}
			if len(names) != 1 || strings.Contains(param.Value, "->") {
				add("CICADA-UNSUPPORTED", "insert chain on track "+track.Name+" is not implemented", "error", param.ValuePosition)
				continue
			}
			effect, ok := effects[names[0]]
			if !ok {
				add("CICADA-REFERENCE", "track references undeclared insert "+names[0], "error", param.ValuePosition)
			} else if effect.Kind != "drive" {
				add("CICADA-UNSUPPORTED", effect.Kind+" as a track insert is not implemented", "error", param.ValuePosition)
			}
		case "send":
			effect, ok := effects[param.Target]
			if !ok {
				if param.Target == "music" || param.Target == "sfx" || namespace[param.Target] == "bus" {
					add("CICADA-UNSUPPORTED", "send to bus "+param.Target+" is not implemented", "error", param.Position)
				} else {
					add("CICADA-REFERENCE", "send references unknown effect or bus "+param.Target, "error", param.Position)
				}
				continue
			}
			if effect.Kind != "delay" && effect.Kind != "reverb" {
				add("CICADA-UNSUPPORTED", "send to "+effect.Kind+" effect "+param.Target+" is not implemented", "error", param.Position)
			}
			validateSendLevel(param.Value, param.ValuePosition, add)
		case "out":
			if param.Value != "music" && param.Value != "sfx" {
				add("CICADA-UNSUPPORTED", "output bus "+param.Value+" is not implemented", "error", param.ValuePosition)
			}
		case "bus":
			if param.Value != "music" && param.Value != "sfx" {
				add("CICADA-UNSUPPORTED", "output bus "+param.Value+" is not implemented", "error", param.ValuePosition)
			}
		case "send_a", "send_b":
			value, err := strconv.ParseFloat(param.Value, 64)
			if err != nil || !finiteMixerNumber(value) || value < 0 || value > 1 {
				add("CICADA-PARAM", param.Name+" must be a unitless value from 0 to 1", "error", param.ValuePosition)
			}
			kind := "delay"
			if param.Name == "send_b" {
				kind = "reverb"
			}
			found := false
			for _, effect := range effects {
				found = found || effect.Kind == kind
			}
			if value > 0 && !found {
				add("CICADA-REFERENCE", param.Name+" requires a declared "+kind+" effect", "error", param.ValuePosition)
			}
		case "send_pre":
			if _, ok := mixerSwitch(param.Value); !ok {
				add("CICADA-PARAM", "send_pre must be true or false", "error", param.ValuePosition)
			}
			legacyPre = param.Value == "true" || param.Value == "on"
		}
	}
	_ = insertSeen
	if legacyPre {
		hasSend := false
		for _, param := range track.Params {
			if param.Name == "send_a" || param.Name == "send_b" {
				value, _ := strconv.ParseFloat(param.Value, 64)
				hasSend = hasSend || value > 0
			}
		}
		if !hasSend {
			add("CICADA-PARAM", "send_pre requires a nonzero send", "error", track.Position)
		}
	}
}

func validateSendLevel(source string, position Position, add func(string, string, string, Position)) {
	value, unit, err := notationBaseValue(source)
	if err != nil || !finiteMixerNumber(value) {
		add("CICADA-PARAM", "send level must be a finite unitless value or dB", "error", position)
		return
	}
	if unit == "unit" {
		if value < 0 || value > 1 {
			add("CICADA-PARAM", "unitless send level must be 0 to 1", "error", position)
		}
		return
	}
	if unit == "db" && value >= -60 && value <= 0 {
		return
	}
	add("CICADA-PARAM", "send level must be 0 to 1 linear or -60 to 0 dB", "error", position)
}

func mixerSwitch(source string) (bool, bool) {
	switch source {
	case "on", "true":
		return true, true
	case "off", "false":
		return false, true
	default:
		return false, false
	}
}

func finiteMixerNumber(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func validKeyRoot(s string) bool {
	if len(s) < 1 || len(s) > 2 || s[0] < 'a' || s[0] > 'g' {
		return false
	}
	return len(s) == 1 || s[1] == '#' || s[1] == 'b'
}

func scoreHasSampler(s *Score, name string) bool {
	for _, sampler := range s.Samplers {
		if sampler.Name == name {
			return true
		}
	}
	return false
}
func scoreHasClip(s *Score, name string) bool {
	for _, clip := range s.Clips {
		if clip.Name == name {
			return true
		}
	}
	return false
}

// ValidateLive checks the declared host controls, retaining source positions.
func ValidateLive(live *Live, tracks []Track) []Diagnostic {
	if live == nil {
		return nil
	}
	var ds []Diagnostic
	add := func(code, message string, p Position) {
		if p.Line == 0 {
			p = live.Position
		}
		if p.Line == 0 {
			p = Position{1, 1}
		}
		ds = append(ds, Diagnostic{Code: code, Severity: "error", Message: message, Position: p})
	}
	unit := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
	}
	switch live.Land {
	case "", "now", "beat", "bar", "2bars", "4bars", "phrase":
	default:
		add("CICADA-LIVE-LAND", "land must be now, beat, bar, 2bars, 4bars, or phrase", live.LandPosition)
	}
	if live.PhraseBars != 0 || live.PhrasePosition.Line != 0 {
		if live.PhraseBars < 1 || live.PhraseBars > 64 {
			add("CICADA-LIVE-PHRASE", "phrase must be 1 to 64 bars", live.PhrasePosition)
		}
	}
	if len(live.Macros) > 16 {
		add("CICADA-LIVE-LIMIT", "live block allows at most 16 macros", live.Macros[16].Position)
	}
	macros := map[string]bool{}
	for _, macro := range live.Macros {
		if macros[macro.Name] {
			add("CICADA-LIVE-MACRO", "duplicate macro "+macro.Name, macro.Position)
		}
		if len(macro.Name) == 0 || len(macro.Name) > 64 {
			add("CICADA-LIVE-MACRO", "macro name must contain 1 to 64 bytes", macro.Position)
		}
		macros[macro.Name] = true
		if !unit(macro.Value) {
			add("CICADA-LIVE-MACRO", "macro value must be a unitless number from 0 to 1", macro.ValuePosition)
		}
		if math.IsNaN(macro.SmoothMS) || math.IsInf(macro.SmoothMS, 0) || macro.SmoothMS < 0 {
			add("CICADA-LIVE-MACRO", "smooth must be a nonnegative duration in ms or s", macro.SmoothPosition)
		}
	}
	trackNames := map[string]bool{}
	for _, track := range tracks {
		trackNames[track.Name] = true
	}
	layerMacros := map[string]bool{}
	for _, layers := range live.Layers {
		if !macros[layers.Macro] {
			add("CICADA-LIVE-MACRO", "unknown macro "+layers.Macro, layers.MacroPosition)
		}
		if layerMacros[layers.Macro] {
			add("CICADA-LIVE-MACRO", "duplicate layers for macro "+layers.Macro, layers.MacroPosition)
		}
		layerMacros[layers.Macro] = true
		if layers.AttackBars != 1 {
			add("CICADA-LIVE-ATTACK", "only attack 1bar is supported", layers.AttackPosition)
		}
		if layers.ReleaseBars < 1 || layers.ReleaseBars > 16 {
			add("CICADA-LIVE-RELEASE", "release must be 1 to 16 bars", layers.ReleasePosition)
		}
		thresholds := map[float64]bool{}
		seenTracks := map[string]bool{}
		for _, rule := range layers.Rules {
			if !trackNames[rule.Track] {
				add("CICADA-LIVE-TRACK", "unknown track "+rule.Track, rule.Position)
			}
			if seenTracks[rule.Track] {
				add("CICADA-LIVE-TRACK", "duplicate layer rule for track "+rule.Track, rule.Position)
			}
			seenTracks[rule.Track] = true
			if !unit(rule.Value) {
				add("CICADA-LIVE-MACRO", "layer threshold must be a unitless number from 0 to 1", rule.ValuePosition)
				continue
			}
			if !thresholds[rule.Value] && len(thresholds) == 3 {
				add("CICADA-LIVE-LIMIT", "layers allow at most 3 distinct thresholds", rule.ValuePosition)
			}
			thresholds[rule.Value] = true
		}
	}
	return ds
}
