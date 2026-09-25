// Package project lowers Cicada notation to fixed-size kernel data.
package project

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

var scaleIntervals = map[string][7]int{
	"major":    {0, 2, 4, 5, 7, 9, 11},
	"minor":    {0, 2, 3, 5, 7, 8, 10},
	"dorian":   {0, 2, 3, 5, 7, 9, 10},
	"phrygian": {0, 1, 3, 5, 7, 8, 10},
	"harmonic": {0, 2, 3, 5, 7, 8, 11},
	"mixo":     {0, 2, 4, 5, 7, 9, 10},
	"pent":     {0, 0, 3, 5, 7, 0, 10},
	"blues":    {0, 0, 3, 5, 7, 0, 10},
}

var chromatic = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}

func parseOctaveLiteral(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > 6 {
		return 0, fmt.Errorf("invalid octave %q: expected integer 0 to 6", value)
	}
	return n, nil
}

var drumNotes = func() map[string]uint8 {
	notes := make(map[string]uint8, drum.LaneCount)
	for lane, name := range drum.Names {
		notes[name] = drum.MIDINotes[lane]
	}
	return notes
}()

type CompiledPattern struct {
	Name    string
	Kind    string
	Lane    string // set for drums
	Pattern seq.Pattern
}

type patternCompileError struct {
	position notation.Position
	err      error
}

func (e *patternCompileError) Error() string { return e.err.Error() }
func (e *patternCompileError) Unwrap() error { return e.err }

// CompilePattern lowers one validated source pattern. Drum lanes become one
// kernel pattern each, so the engine can assign a voice to each lane.
func CompilePattern(score *notation.Score, source notation.Pattern, track notation.Track) ([]CompiledPattern, error) {
	kitTrack := false
	for _, kit := range score.Kits {
		if kit.Name == track.Kind {
			kitTrack = true
			break
		}
	}
	compatible := ((track.Kind == "drums" || kitTrack) && source.Kind == "drums") ||
		(track.Kind == "acid" && (source.Kind == "acid" || source.Kind == "notes")) ||
		(track.Kind != "acid" && track.Kind != "drums" && !kitTrack && source.Kind == "notes")
	if !compatible {
		return nil, fmt.Errorf("pattern %s kind differs from track %s", source.Name, track.Name)
	}
	count := len(source.Steps)
	if source.Kind == "drums" && len(source.Lanes) > 0 {
		count = len(source.Lanes[0].Hits)
	}
	if count < 1 || count > 64 {
		return nil, fmt.Errorf("pattern %s must have 1 to 64 steps", source.Name)
	}
	if score.Seed > 1<<32-1 {
		return nil, fmt.Errorf("project seed exceeds 32-bit kernel seed")
	}
	base := seq.Pattern{Len: uint8(count), GatePercent: 55, Seed: uint32(score.Seed)}
	if track.Kind == "acid" {
		for _, param := range track.Params {
			if param.Name == "gate" {
				value, err := strconv.Atoi(strings.TrimSuffix(param.Value, "%"))
				if err != nil || value < 10 || value > 100 {
					return nil, fmt.Errorf("invalid track gate %q", param.Value)
				}
				base.GatePercent = uint8(value)
			}
		}
	}
	for _, attr := range source.Attrs {
		switch attr.Name {
		case "swing":
			percent100, err := parsePercent100(attr.Value)
			if err != nil {
				return nil, err
			}
			base.SwingPermille, err = seq.SwingFromPercent100(percent100)
			if err != nil {
				return nil, err
			}
		case "gate":
			n, err := strconv.Atoi(strings.TrimSuffix(attr.Value, "%"))
			if err != nil || n < 10 || n > 100 {
				return nil, fmt.Errorf("invalid gate %q", attr.Value)
			}
			base.GatePercent = uint8(n)
		case "transpose":
			n, err := strconv.Atoi(attr.Value)
			if err != nil || n < -24 || n > 24 {
				return nil, fmt.Errorf("invalid transpose %q", attr.Value)
			}
			base.Transpose = int8(n)
		case "seed":
			n, err := strconv.ParseUint(attr.Value, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("invalid seed %q", attr.Value)
			}
			base.Seed = uint32(n)
		}
	}
	if source.Kind == "acid" || source.Kind == "notes" {
		octave := 2
		for _, inst := range score.Instruments {
			if inst.Name == track.Kind {
				octave = inst.Octave
				break
			}
		}
		for _, param := range track.Params {
			if param.Name == "octave" {
				n, err := parseOctaveLiteral(param.Value)
				if err != nil {
					return nil, err
				}
				octave = n
			}
		}
		for i, token := range source.Steps {
			step, err := acidStep(score, token.Text, octave, token.Transpose)
			if err != nil {
				return nil, &patternCompileError{position: token.Position, err: err}
			}
			base.Steps[i], err = seq.PackStep(step)
			if err != nil {
				return nil, &patternCompileError{position: token.Position, err: err}
			}
		}
		return []CompiledPattern{{Name: source.Name, Kind: source.Kind, Pattern: base}}, nil
	}
	compiled := make([]CompiledPattern, 0, len(source.Lanes))
	for _, lane := range source.Lanes {
		if _, ok := drumLane(lane.Name); !ok {
			return nil, fmt.Errorf("unsupported drum lane %s", lane.Name)
		}
		note, ok := drumNotes[lane.Name]
		if !ok {
			return nil, fmt.Errorf("unknown drum lane %s", lane.Name)
		}
		p := base
		for i, token := range lane.Hits {
			step, err := drumStep(token.Text, note)
			if err != nil {
				return nil, &patternCompileError{position: token.Position, err: err}
			}
			p.Steps[i], err = seq.PackStep(step)
			if err != nil {
				return nil, &patternCompileError{position: token.Position, err: err}
			}
		}
		compiled = append(compiled, CompiledPattern{Name: source.Name, Kind: "drums", Lane: lane.Name, Pattern: p})
	}
	return compiled, nil
}

func acidStep(score *notation.Score, token string, octave, transpose int) (seq.Step, error) {
	s := seq.Step{Ratchet: 1, Probability: 100, Velocity: 100}
	if token == "." {
		return s, nil
	}
	if token == "-" {
		s.Gate, s.Tie = true, true
		return s, nil
	}
	if token == "" {
		return s, fmt.Errorf("empty acid step")
	}
	note, consumed, err := parsePitch(score, token, octave)
	if err != nil {
		return s, err
	}
	for consumed < len(token) {
		switch token[consumed] {
		case '\'':
			note += 12
			consumed++
		case ',':
			note -= 12
			consumed++
		case '^':
			s.Accent = true
			consumed++
		case '~':
			s.Slide = true
			consumed++
		case '*', '%', '?':
			kind := token[consumed]
			consumed++
			start := consumed
			for consumed < len(token) && token[consumed] >= '0' && token[consumed] <= '9' {
				consumed++
			}
			n, err := strconv.Atoi(token[start:consumed])
			if err != nil {
				return s, fmt.Errorf("invalid modifier in %q", token)
			}
			if kind == '*' {
				if n < 2 || n > 8 {
					return s, fmt.Errorf("ratchet must be 2 to 8")
				}
				s.Ratchet = uint8(n)
			} else {
				if n < 1 || n > 99 {
					return s, fmt.Errorf("probability must be 1 to 99")
				}
				s.Probability = uint8(n)
			}
		default:
			return s, fmt.Errorf("invalid acid modifier in %q", token)
		}
	}
	note += transpose
	if note < 0 || note > 127 {
		return s, fmt.Errorf("pitch %q outside MIDI range", token)
	}
	s.Note, s.Gate = uint8(note), true
	return s, nil
}

func parsePitch(score *notation.Score, token string, octave int) (note, consumed int, err error) {
	first := token[0]
	consumed = 1
	if first >= '1' && first <= '7' {
		intervals, ok := scaleIntervals[score.Scale]
		if !ok {
			return 0, 0, fmt.Errorf("unknown scale %q", score.Scale)
		}
		if (score.Scale == "pent" || score.Scale == "blues") && (first == '2' || first == '6') {
			return 0, 0, fmt.Errorf("scale %s has no degree %c", score.Scale, first)
		}
		root := chromatic[score.KeyRoot[0]]
		if len(score.KeyRoot) == 2 {
			if score.KeyRoot[1] == '#' {
				root++
			} else {
				root--
			}
		}
		note = 12*(octave+1) + root + intervals[int(first-'1')]
	} else {
		semi, ok := chromatic[first]
		if !ok {
			return 0, 0, fmt.Errorf("invalid pitch %q", token)
		}
		note = 12*(octave+1) + semi
	}
	if consumed < len(token) && (token[consumed] == '#' || token[consumed] == 'b') {
		if token[consumed] == '#' {
			note++
		} else {
			note--
		}
		consumed++
	}
	if first >= 'a' && first <= 'g' && consumed < len(token) && token[consumed] >= '0' && token[consumed] <= '6' {
		note = note - 12*(octave+1) + 12*int(token[consumed]-'0'+1)
		consumed++
	}
	return note, consumed, nil
}

func drumStep(token string, note uint8) (seq.Step, error) {
	s := seq.Step{Note: note, Ratchet: 1, Probability: 100}
	if token == "." {
		return s, nil
	}
	if token == "" || (token[0] != 'x' && token[0] != 'X') {
		return s, fmt.Errorf("invalid drum hit %q", token)
	}
	s.Gate = true
	s.Velocity = 100
	if token[0] == 'X' {
		s.Accent, s.Velocity = true, 127
	}
	i := 1
	if i < len(token) && token[i] >= '1' && token[i] <= '9' {
		s.Velocity = uint8(token[i]-'0') * 14
		i++
	}
	for i < len(token) {
		kind := token[i]
		if kind != '*' && kind != '%' && kind != '?' {
			return s, fmt.Errorf("invalid drum modifier in %q", token)
		}
		i++
		start := i
		for i < len(token) && token[i] >= '0' && token[i] <= '9' {
			i++
		}
		n, err := strconv.Atoi(token[start:i])
		if err != nil {
			return s, fmt.Errorf("invalid drum modifier in %q", token)
		}
		if kind == '*' {
			if n < 2 || n > 8 {
				return s, fmt.Errorf("ratchet must be 2 to 8")
			}
			s.Ratchet = uint8(n)
		} else {
			if n < 1 || n > 99 {
				return s, fmt.Errorf("probability must be 1 to 99")
			}
			s.Probability = uint8(n)
		}
	}
	return s, nil
}

func parsePercent100(s string) (uint16, error) {
	s = strings.TrimSuffix(s, "%")
	parts := strings.SplitN(s, ".", 2)
	whole, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid swing %q", s)
	}
	frac := 0
	if len(parts) == 2 {
		if len(parts[1]) > 2 {
			return 0, fmt.Errorf("swing supports at most two decimals")
		}
		frac, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, fmt.Errorf("invalid swing %q", s)
		}
		if len(parts[1]) == 1 {
			frac *= 10
		}
	}
	return uint16(whole*100 + frac), nil
}
