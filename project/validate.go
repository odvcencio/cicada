package project

import (
	"fmt"
	"math"
	"unicode/utf8"
)

var acceptedScales = map[string]bool{
	"minor": true, "major": true, "dorian": true, "phrygian": true,
	"harmonic": true, "pent": true, "mixo": true, "blues": true,
}

// ValidateProject checks the semantic interchange records after source lowering
// or JSON decoding. It does not yet load audio into the runtime engine.
func ValidateProject(p *Project) error {
	if p == nil || p.Format != FormatID || p.Version != 1 {
		return fmt.Errorf("unsupported project format or version")
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
	if len(p.Kits) != 0 || len(p.Effects) != 0 {
		return fmt.Errorf("kits and effects are not implemented in M0")
	}
	if len(p.Tracks) < 1 || len(p.Tracks) > 16 || len(p.Patterns) == 0 || len(p.Song) == 0 {
		return fmt.Errorf("project needs 1 to 16 tracks, patterns, and a song")
	}
	instruments := map[string]bool{}
	for _, inst := range p.Instruments {
		if err := uniqueID(inst.ID, instruments); err != nil {
			return fmt.Errorf("instrument: %w", err)
		}
		if inst.Mode != "mono" || inst.Params == nil || inst.Lets == nil {
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
	}
	tracks := map[string]Track{}
	for _, track := range p.Tracks {
		if _, exists := tracks[track.ID]; exists || !validID(track.ID) {
			return fmt.Errorf("duplicate or invalid track ID %q", track.ID)
		}
		tracks[track.ID] = track
		if track.Kind != "acid" && track.Kind != "drums" && !instruments[track.Kind] {
			return fmt.Errorf("track %s has unknown instrument %s", track.ID, track.Kind)
		}
		if track.Params == nil {
			return fmt.Errorf("track %s params must be explicit", track.ID)
		}
		for name, value := range track.Params {
			if !validID(name) || !validValue(value) {
				return fmt.Errorf("track %s has invalid parameter %s", track.ID, name)
			}
		}
		if track.Mixer != defaultMixer() {
			return fmt.Errorf("track %s uses unsupported mixer settings", track.ID)
		}
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
			if len(pattern.Data) != 0 || len(pattern.Lanes) != len(laneOrder) {
				return fmt.Errorf("drum pattern %s needs all eleven lane arrays", pattern.ID)
			}
			for _, lane := range laneOrder {
				steps, ok := pattern.Lanes[lane]
				if !ok || len(steps) != int(pattern.Steps) {
					return fmt.Errorf("drum pattern %s lane %s has wrong length", pattern.ID, lane)
				}
				if err := validateSteps(steps); err != nil {
					return fmt.Errorf("drum pattern %s lane %s: %w", pattern.ID, lane, err)
				}
			}
		} else if pattern.Kind == "acid" || pattern.Kind == "notes" {
			if len(pattern.Data) != int(pattern.Steps) || len(pattern.Lanes) != 0 {
				return fmt.Errorf("note pattern %s has wrong data length", pattern.ID)
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
			if !ok || seen[*slot] || !compatible(track.Kind, pattern.Kind) {
				return fmt.Errorf("track %s has invalid or duplicate slot %s", track.ID, *slot)
			}
			seen[*slot] = true
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
			pattern, ok := patterns[patternID]
			if !ok || !compatible(track.Kind, pattern.Kind) || !hasSlot(track, patternID) {
				return fmt.Errorf("scene %s cannot bind %s to %s", scene.ID, trackID, patternID)
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
		for trackID, patternID := range active {
			if tracks[trackID].Kind == "drums" {
				for _, steps := range patterns[patternID].Lanes {
					for _, step := range steps {
						if step != nil {
							voices++
							break
						}
					}
				}
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

func validID(id string) bool {
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
	return unit == "unit" || unit == "hz" || unit == "ms" || unit == "db"
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
	case "+", "-", "*", "/", "env", "lowpass", "highpass":
		return n == 2
	case "saw", "square", "sine", "tanh", "exp2":
		return n == 1
	case "noise":
		return n == 0
	case "ladder", "diode", "mix", "clamp":
		return n == 3
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
		if step.Tie && (step.Note != 0 || step.Ratchet != 1 || step.Probability != 100) {
			return fmt.Errorf("step %d has invalid tie encoding", index)
		}
	}
	return nil
}

func compatible(trackKind, patternKind string) bool {
	if trackKind == "drums" {
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
