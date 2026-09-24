package notation

import (
	"strconv"
	"strings"
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
}

var drumParams = map[string]map[string]bool{
	"bd": {"tune": true, "decay": true, "sweep": true, "sweep_time": true, "click": true, "drive": true},
	"sd": {"tune": true, "tone": true, "mix": true, "snappy": true, "decay": true},
	"ch": {"tune": true, "decay": true, "tone": true, "metal": true},
	"oh": {"tune": true, "decay": true, "tone": true, "metal": true},
	"cp": {"tone": true, "decay": true, "spread": true},
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
	var ds []Diagnostic
	add := func(code, message, severity string, p Position) {
		ds = append(ds, Diagnostic{Code: code, Message: message, Severity: severity, Position: p})
	}
	if s.Version != 1 {
		add("CICADA-VERSION", "only cicada 1 is supported", "error", Position{1, 1})
	}
	if s.TempoMilli < 20_000 || s.TempoMilli > 300_000 {
		add("CICADA-TEMPO", "tempo must be 20 to 300 BPM with at most three decimals", "error", Position{1, 1})
	}
	if !validKeyRoot(s.KeyRoot) || !scales[s.Scale] {
		add("CICADA-KEY", "unknown key or scale", "error", Position{1, 1})
	}
	if len(s.Tracks) == 0 || len(s.Tracks) > 16 {
		add("CICADA-TRACKS", "score must have 1 to 16 tracks", "error", Position{1, 1})
	}
	instruments := make(map[string]Instrument, len(s.Instruments))
	for _, inst := range s.Instruments {
		if inst.Name == "acid" || inst.Name == "drums" {
			add("CICADA-INSTRUMENT", "instrument name is reserved: "+inst.Name, "error", inst.Position)
		}
		if _, exists := instruments[inst.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate instrument "+inst.Name, "error", inst.Position)
		}
		instruments[inst.Name] = inst
		if inst.Mode != "mono" && inst.Mode != "poly" {
			add("CICADA-VOICE", "voice mode must be mono or poly", "error", inst.Position)
		}
		if inst.Output == nil {
			add("CICADA-VOICE", "voice needs an out expression", "error", inst.Position)
		}
		seen := map[string]bool{}
		for _, param := range inst.Params {
			if seen[param.Name] {
				add("CICADA-DUPLICATE", "duplicate instrument parameter "+param.Name, "error", param.Position)
			}
			seen[param.Name] = true
			if param.Unit != "hz" && param.Unit != "ms" && param.Unit != "unit" && param.Unit != "db" {
				add("CICADA-UNIT", "instrument parameter unit must be hz, ms, unit, or db", "error", param.Position)
			}
		}
	}
	trackByName := make(map[string]Track, len(s.Tracks))
	for _, t := range s.Tracks {
		if _, exists := trackByName[t.Name]; exists {
			add("CICADA-DUPLICATE", "duplicate track "+t.Name, "error", t.Position)
		}
		trackByName[t.Name] = t
		if t.Kind != "acid" && t.Kind != "drums" {
			if _, ok := instruments[t.Kind]; !ok {
				add("CICADA-TRACK-KIND", "unknown instrument "+t.Kind, "error", t.Position)
			}
		}
		seen := map[string]bool{}
		for _, param := range t.Params {
			if seen[param.Name] {
				add("CICADA-DUPLICATE", "duplicate parameter "+param.Name, "error", param.Position)
			}
			seen[param.Name] = true
			if !validTrackParam(t.Kind, param.Name, instruments) {
				add("CICADA-PARAM", "unknown parameter "+param.Name, "error", param.Position)
			}
		}
	}
	if len(s.Patterns) == 0 {
		add("CICADA-PATTERNS", "score needs at least one pattern", "error", Position{1, 1})
	}
	patterns := make(map[string]Pattern, len(s.Patterns))
	for _, p := range s.Patterns {
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
			add("CICADA-STEPS", "pattern must have 1 to 64 steps", "error", p.Position)
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
					add("CICADA-STEPS", "steps attribute must equal the number of cells", "error", a.Position)
				}
			case "swing":
				n := parseMilli(a.Value)
				if n < 50_000 || n > 75_000 {
					add("CICADA-SWING", "swing must be 50 to 75 percent", "error", a.Position)
				}
			case "gate":
				n, err := strconv.Atoi(a.Value)
				if err != nil || n < 10 || n > 100 {
					add("CICADA-GATE", "gate must be 10 to 100 percent", "error", a.Position)
				}
			case "seed", "slot", "transpose":
				// Numeric domains are checked during compilation to packed fields.
			default:
				add("CICADA-ATTRIBUTE", "unknown pattern attribute "+a.Name, "error", a.Position)
			}
		}
		if p.Kind == "acid" || p.Kind == "notes" {
			for i, token := range p.Steps {
				if strings.Contains(token.Text, "~") && p.Steps[(i+1)%len(p.Steps)].Text == "." {
					add("CICADA-SLIDE-REST", "slide into a rest has no effect", "warning", token.Position)
				}
			}
		} else {
			seenLanes := map[string]bool{}
			for _, lane := range p.Lanes {
				if _, ok := drumParams[lane.Name]; !ok {
					add("CICADA-LANE", "unknown drum lane "+lane.Name, "error", lane.Position)
				}
				if seenLanes[lane.Name] {
					add("CICADA-DUPLICATE", "duplicate drum lane "+lane.Name, "error", lane.Position)
				}
				seenLanes[lane.Name] = true
				if len(lane.Hits) != steps {
					add("CICADA-STEPS", "drum lane length differs from pattern", "error", lane.Position)
				}
			}
		}
	}
	scenes := make(map[string]bool, len(s.Scenes))
	for _, scene := range s.Scenes {
		if scenes[scene.Name] {
			add("CICADA-DUPLICATE", "duplicate scene "+scene.Name, "error", scene.Position)
		}
		scenes[scene.Name] = true
		seenTracks := map[string]bool{}
		for _, b := range scene.Bindings {
			track, trackOK := trackByName[b.Track]
			if !trackOK {
				add("CICADA-REFERENCE", "scene references unknown track "+b.Track, "error", b.Position)
			}
			if seenTracks[b.Track] {
				add("CICADA-DUPLICATE", "scene assigns track twice: "+b.Track, "error", b.Position)
			}
			seenTracks[b.Track] = true
			if b.Pattern == "keep" || b.Pattern == "off" {
				continue
			}
			pattern, patternOK := patterns[b.Pattern]
			if !patternOK {
				add("CICADA-REFERENCE", "scene references unknown pattern "+b.Pattern, "error", b.Position)
			} else if trackOK {
				compatible := (track.Kind == "drums" && pattern.Kind == "drums") ||
					(track.Kind == "acid" && (pattern.Kind == "acid" || pattern.Kind == "notes")) ||
					(track.Kind != "acid" && track.Kind != "drums" && pattern.Kind == "notes")
				if !compatible {
					add("CICADA-KIND", "pattern kind differs from track instrument", "error", b.Position)
				}
			}
		}
	}
	for _, entry := range s.Song {
		if !scenes[entry.Scene] {
			add("CICADA-REFERENCE", "song references unknown scene "+entry.Scene, "error", entry.Position)
		}
		if entry.Bars < 1 || entry.Bars > 999 {
			add("CICADA-BARS", "song entry must be 1 to 999 bars", "error", entry.Position)
		}
	}
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
		return len(parts) == 2 && drumParams[parts[0]][parts[1]]
	}
	if inst, ok := instruments[kind]; ok {
		for _, param := range inst.Params {
			if param.Name == name {
				return true
			}
		}
	}
	return false
}

func validKeyRoot(s string) bool {
	if len(s) < 1 || len(s) > 2 || s[0] < 'a' || s[0] > 'g' {
		return false
	}
	return len(s) == 1 || s[1] == '#' || s[1] == 'b'
}
