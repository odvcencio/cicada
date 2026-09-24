package project

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

const FormatID = "cicada.project/1"

// Project is the semantic interchange model shared by source tooling and the
// future workstation. Slice order follows source order; maps carry keyed data.
type Project struct {
	Format      string       `json:"format"`
	Version     int          `json:"version"`
	Title       string       `json:"title"`
	TempoMilli  int          `json:"tempo_milli"`
	Key         Key          `json:"key"`
	Seed        uint32       `json:"seed"`
	Instruments []Instrument `json:"instruments"`
	Kits        []Kit        `json:"kits"`
	Tracks      []Track      `json:"tracks"`
	Patterns    []Pattern    `json:"patterns"`
	Scenes      []Scene      `json:"scenes"`
	Song        []SongEntry  `json:"song"`
	Effects     []Effect     `json:"effects"`
}

type Key struct {
	Root  uint8  `json:"root"`
	Scale string `json:"scale"`
}

type Instrument struct {
	ID     string            `json:"id"`
	Mode   string            `json:"mode"`
	Params []InstrumentParam `json:"params"`
	Lets   []Binding         `json:"lets"`
	Out    Expr              `json:"out"`
}

type InstrumentParam struct {
	ID      string  `json:"id"`
	Unit    string  `json:"unit"`
	Default float64 `json:"default"`
}

type Binding struct {
	ID    string `json:"id"`
	Value Expr   `json:"value"`
}

// Expr is encoded as exactly one of literal, name, or op+args.
type Expr struct {
	Op      string   `json:"op"`
	Args    []Expr   `json:"args"`
	Literal *float64 `json:"literal"`
	Name    string   `json:"name"`
}

type Kit struct {
	ID    string            `json:"id"`
	Lanes map[string]string `json:"lanes"`
}

type Track struct {
	ID     string           `json:"id"`
	Kind   string           `json:"kind"`
	Params map[string]Value `json:"params"`
	Mixer  Mixer            `json:"mixer"`
	Slots  [16]*string      `json:"slots"`
}

type Value struct {
	Unit   string   `json:"unit"`
	Number *float64 `json:"number"`
	Text   string   `json:"text"`
}

type Mixer struct {
	GainDB  float64 `json:"gain_db"`
	Pan     float64 `json:"pan"`
	SendA   float64 `json:"send_a"`
	SendB   float64 `json:"send_b"`
	SendPre bool    `json:"send_pre"`
	Mute    bool    `json:"mute"`
	Solo    bool    `json:"solo"`
	Insert  string  `json:"insert"`
	Bus     string  `json:"bus"`
}

type Pattern struct {
	ID              string             `json:"id"`
	Kind            string             `json:"kind"`
	Steps           uint8              `json:"steps"`
	SwingPercent100 uint16             `json:"swing_percent100"`
	GatePercent     uint8              `json:"gate_percent"`
	Transpose       int8               `json:"transpose"`
	Seed            uint32             `json:"seed"`
	Data            []*Step            `json:"data"`
	Lanes           map[string][]*Step `json:"lanes"`
}

type Step struct {
	Note        uint8 `json:"note"`
	Accent      bool  `json:"accent"`
	Slide       bool  `json:"slide"`
	Tie         bool  `json:"tie"`
	Ratchet     uint8 `json:"ratchet"`
	Probability uint8 `json:"probability"`
	Velocity    uint8 `json:"velocity"`
}

type Scene struct {
	ID       string            `json:"id"`
	Bindings map[string]string `json:"bindings"`
}

type SongEntry struct {
	Scene string `json:"scene"`
	Bars  uint16 `json:"bars"`
}

type Effect struct {
	ID     string           `json:"id"`
	Params map[string]Value `json:"params"`
}

var laneOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}

func defaultMixer() Mixer { return Mixer{GainDB: -6, Insert: "none", Bus: "music"} }

// FromScore lowers a validated source score to the versioned semantic model.
// It does not silently omit a source declaration that has no v1 representation.
func FromScore(score *notation.Score) (*Project, []notation.Diagnostic) {
	if score == nil {
		return nil, []notation.Diagnostic{{Code: "CICADA-SYNTAX", Severity: "error", Message: "nil score", Position: notation.Position{Line: 1, Column: 1}}}
	}
	diagnostics := notation.Validate(score)
	_, compiledDiagnostics := Check(score)
	diagnostics = append(diagnostics, compiledDiagnostics...)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, diagnostics
		}
	}
	if utf8.RuneCountInString(score.Title) > 120 {
		return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-LIMIT", Severity: "error", Message: "title exceeds 120 characters", Position: score.TitlePosition})
	}
	root, err := rootPitchClass(score.KeyRoot)
	if err != nil {
		return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-KEY", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	p := &Project{
		Format: FormatID, Version: 1, Title: score.Title, TempoMilli: int(score.TempoMilli),
		Key: Key{Root: root, Scale: score.Scale}, Seed: uint32(score.Seed),
		Instruments: []Instrument{}, Kits: []Kit{}, Tracks: []Track{}, Patterns: []Pattern{},
		Scenes: []Scene{}, Song: []SongEntry{}, Effects: []Effect{},
	}
	for _, source := range score.Instruments {
		inst := Instrument{ID: source.Name, Mode: source.Mode, Params: []InstrumentParam{}, Lets: []Binding{}}
		for _, param := range source.Params {
			value, _, err := parseBaseValue(param.Default)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-UNIT", Severity: "error", Message: err.Error(), Position: param.Position})
			}
			inst.Params = append(inst.Params, InstrumentParam{ID: param.Name, Unit: param.Unit, Default: value})
		}
		for _, binding := range source.Lets {
			inst.Lets = append(inst.Lets, Binding{ID: binding.Name, Value: projectExpr(binding.Value)})
		}
		inst.Out = projectExpr(source.Output)
		p.Instruments = append(p.Instruments, inst)
	}
	for _, source := range score.Kits {
		kit := Kit{ID: source.Name, Lanes: map[string]string{}}
		for _, binding := range source.Bindings {
			kit.Lanes[binding.Lane] = binding.Target
		}
		p.Kits = append(p.Kits, kit)
	}
	for _, source := range score.Effects {
		effect := Effect{ID: source.Name, Params: map[string]Value{}}
		for _, param := range source.Params {
			value, err := projectValue(param.Value)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
			}
			effect.Params[param.Name] = value
			if source.Name == "drive" {
				if _, err := DriveParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if source.Name == "delay" {
				if _, err := DelayParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if source.Name == "reverb" {
				if _, err := ReverbParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if source.Name == "comp" {
				if _, _, err := CompSpecFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			}
		}
		p.Effects = append(p.Effects, effect)
	}
	for _, source := range score.Tracks {
		track := Track{ID: source.Name, Kind: source.Kind, Params: map[string]Value{}, Mixer: defaultMixer()}
		mixer, err := CompileMixerParams(source)
		if err != nil {
			return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: source.Position})
		}
		track.Mixer = mixer
		for _, param := range source.Params {
			if param.Name == "level" || param.Name == "pan" || param.Name == "insert" || param.Name == "send_a" || param.Name == "send_b" || param.Name == "send_pre" {
				continue
			}
			value, err := projectValue(param.Value)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
			}
			track.Params[param.Name] = value
		}
		p.Tracks = append(p.Tracks, track)
	}
	for _, source := range score.Patterns {
		track := representativeTrack(score, source)
		compiled, err := CompilePattern(score, source, track)
		if err != nil {
			return nil, append(diagnostics, patternCompileDiagnostic(err, source.Position))
		}
		pattern := Pattern{
			ID: source.Name, Kind: source.Kind, Steps: compiled[0].Pattern.Len,
			SwingPercent100: 5000, GatePercent: compiled[0].Pattern.GatePercent,
			Transpose: compiled[0].Pattern.Transpose, Seed: compiled[0].Pattern.Seed,
			Data: []*Step{}, Lanes: map[string][]*Step{},
		}
		for _, attr := range source.Attrs {
			if attr.Name == "swing" {
				pattern.SwingPercent100, _ = parsePercent100(attr.Value)
			}
		}
		if source.Kind == "drums" {
			for _, lane := range laneOrder {
				pattern.Lanes[lane] = make([]*Step, pattern.Steps)
			}
			for _, lane := range compiled {
				pattern.Lanes[lane.Lane] = projectSteps(lane.Pattern)
			}
		} else {
			pattern.Data = projectSteps(compiled[0].Pattern)
		}
		p.Patterns = append(p.Patterns, pattern)
	}
	for _, source := range score.Scenes {
		scene := Scene{ID: source.Name, Bindings: map[string]string{}}
		for _, binding := range source.Bindings {
			scene.Bindings[binding.Track] = binding.Pattern
		}
		p.Scenes = append(p.Scenes, scene)
	}
	for _, entry := range score.Song {
		p.Song = append(p.Song, SongEntry{Scene: entry.Scene, Bars: uint16(entry.Bars)})
	}
	if err := assignSlots(p, score); err != nil {
		return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-LIMIT", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	if err := ValidateProject(p); err != nil {
		return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	if _, err := canonicalProjectBytes(p); err != nil {
		code := "CICADA-PARAM"
		if errors.Is(err, errCanonicalJSONLimit) {
			code = "CICADA-LIMIT"
		}
		return nil, append(diagnostics, notation.Diagnostic{Code: code, Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	return p, diagnostics
}

func representativeTrack(score *notation.Score, pattern notation.Pattern) notation.Track {
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Pattern == pattern.Name {
				for _, track := range score.Tracks {
					if track.Name == binding.Track {
						return track
					}
				}
			}
		}
	}
	kind := "acid"
	if pattern.Kind == "drums" {
		kind = "drums"
	} else if pattern.Kind == "notes" {
		kind = "unused_notes"
	}
	return notation.Track{Kind: kind}
}

func projectSteps(source seq.Pattern) []*Step {
	steps := make([]*Step, source.Len)
	for i := uint8(0); i < source.Len; i++ {
		decoded, _ := seq.UnpackStep(source.Steps[i])
		if !decoded.Gate {
			continue
		}
		steps[i] = &Step{
			Note: decoded.Note, Accent: decoded.Accent, Slide: decoded.Slide,
			Tie: decoded.Tie, Ratchet: decoded.Ratchet, Probability: decoded.Probability,
			Velocity: decoded.Velocity,
		}
	}
	return steps
}

func assignSlots(p *Project, score *notation.Score) error {
	used := make(map[string]map[string]bool, len(p.Tracks))
	explicit := make(map[string]int, len(score.Patterns))
	for _, pattern := range score.Patterns {
		for _, attr := range pattern.Attrs {
			if attr.Name == "slot" {
				slot, err := strconv.Atoi(attr.Value)
				if err != nil || slot < 0 || slot >= 16 {
					return fmt.Errorf("pattern %s has invalid slot %q", pattern.Name, attr.Value)
				}
				explicit[pattern.Name] = slot
			}
		}
	}
	for _, scene := range p.Scenes {
		for track, pattern := range scene.Bindings {
			if pattern == "off" || pattern == "keep" {
				continue
			}
			if used[track] == nil {
				used[track] = map[string]bool{}
			}
			used[track][pattern] = true
		}
	}
	for ti := range p.Tracks {
		track := &p.Tracks[ti]
		for _, pattern := range p.Patterns {
			if !used[track.ID][pattern.ID] {
				continue
			}
			slot, hasSlot := explicit[pattern.ID]
			if !hasSlot {
				continue
			}
			if prior := track.Slots[slot]; prior != nil {
				return fmt.Errorf("track %s slot %d is assigned to both %s and %s", track.ID, slot, *prior, pattern.ID)
			}
			id := pattern.ID
			track.Slots[slot] = &id
		}
		next := 0
		for _, pattern := range p.Patterns {
			if !used[track.ID][pattern.ID] {
				continue
			}
			if _, hasSlot := explicit[pattern.ID]; hasSlot {
				continue
			}
			for next < len(track.Slots) && track.Slots[next] != nil {
				next++
			}
			if next == len(track.Slots) {
				return fmt.Errorf("track %s uses more than 16 patterns", track.ID)
			}
			id := pattern.ID
			track.Slots[next] = &id
			next++
		}
	}
	return nil
}

func projectExpr(source *notation.Expr) Expr {
	if source == nil {
		return Expr{}
	}
	switch source.Kind {
	case "number":
		value, _, _ := parseBaseValue(source.Text)
		return Expr{Literal: &value}
	case "name":
		return Expr{Name: source.Text}
	case "binary":
		return Expr{Op: source.Text, Args: []Expr{projectExpr(source.Left), projectExpr(source.Right)}}
	case "call":
		out := Expr{Op: source.Text, Args: []Expr{}}
		for _, arg := range source.Args {
			out.Args = append(out.Args, projectExpr(arg))
		}
		return out
	}
	return Expr{}
}

func projectValue(source string) (Value, error) {
	if strings.HasPrefix(source, "\"") {
		value, err := strconv.Unquote(source)
		return Value{Unit: "enum", Text: value}, err
	}
	value, unit, err := parseBaseValue(source)
	if err == nil {
		return Value{Unit: unit, Number: &value}, nil
	}
	if strings.IndexFunc(source, func(r rune) bool { return r == ' ' || r == '\n' }) >= 0 {
		return Value{}, fmt.Errorf("invalid value %q", source)
	}
	return Value{Unit: "enum", Text: source}, nil
}

func parseBaseValue(source string) (float64, string, error) {
	unit := "unit"
	scale := 1.0
	for _, suffix := range []struct {
		name, unit string
		scale      float64
	}{{"khz", "hz", 1000}, {"hz", "hz", 1}, {"ms", "ms", 1}, {"db", "db", 1}, {"s", "ms", 1000}, {"%", "unit", 0.01}} {
		if strings.HasSuffix(source, suffix.name) {
			unit, scale = suffix.unit, suffix.scale
			source = strings.TrimSuffix(source, suffix.name)
			break
		}
	}
	value, err := strconv.ParseFloat(source, 64)
	if err != nil {
		return 0, "", err
	}
	return value * scale, unit, nil
}

func rootPitchClass(source string) (uint8, error) {
	if len(source) == 0 {
		return 0, fmt.Errorf("empty key root")
	}
	root, ok := chromatic[source[0]]
	if !ok {
		return 0, fmt.Errorf("invalid key root %q", source)
	}
	if len(source) == 2 {
		if source[1] == '#' {
			root++
		} else if source[1] == 'b' {
			root--
		}
	}
	return uint8((root + 12) % 12), nil
}
