package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

const FormatID = "cicada.project/1"
const FormatID2 = "cicada.project/2"

// Project is the semantic interchange model shared by source tooling and the
// future workstation. Slice order follows source order; maps carry keyed data.
type Project struct {
	Format      string       `cicada:"Semantic format identifier" json:"format"`
	Version     int          `cicada:"Semantic format version" json:"version"`
	Edition     int          `cicada:"Source language edition" default:"1" json:"edition,omitempty"`
	Title       string       `cicada:"Project title" json:"title"`
	TempoMilli  int          `cicada:"Tempo in thousandths of a beat per minute" unit:"milli-BPM" range:"20000..300000" json:"tempo_milli"`
	Key         Key          `cicada:"Tonal root and scale" json:"key"`
	Seed        uint32       `cicada:"Project random seed" json:"seed"`
	Assets      []Asset      `cicada:"Immutable audio asset table" json:"assets,omitempty" introduced:"cicada.project/2"`
	Clips       []Clip       `cicada:"Audio regions" json:"clips,omitempty" introduced:"cicada.project/2"`
	Samplers    []Sampler    `cicada:"Single-region sampler instruments" json:"samplers,omitempty" introduced:"cicada.project/2"`
	Instruments []Instrument `cicada:"Programmable sound generators" json:"instruments"`
	Kits        []Kit        `cicada:"Drum instrument assignments" json:"kits"`
	Tracks      []Track      `cicada:"Mixer tracks" json:"tracks"`
	Patterns    []Pattern    `cicada:"Reusable step patterns" json:"patterns"`
	Scenes      []Scene      `cicada:"Pattern arrangements" json:"scenes"`
	Song        []SongEntry  `cicada:"Ordered scene playback" json:"song"`
	Effects     []Effect     `cicada:"Project effects" json:"effects"`
	Buses       []Bus        `cicada:"Named mixer buses" json:"buses,omitempty" introduced:"cicada.project/2"`
	Master      *Master      `cicada:"Master mixer" json:"master,omitempty" introduced:"cicada.project/2"`
	Exports     []Export     `cicada:"Named render delivery targets" json:"exports,omitempty" introduced:"cicada.project/2"`
	Live        *Live        `cicada:"Declared host controls" json:"live,omitempty" introduced:"cicada.project/2"`
	p2Syntax    bool
}

type Key struct {
	Root  uint8  `cicada:"Tonal root pitch class" range:"0..11" json:"root"`
	Scale string `cicada:"Scale family" json:"scale"`
}

type Instrument struct {
	ID     string            `cicada:"Instrument identifier" json:"id"`
	Octave *int              `cicada:"Home octave for unnumbered notes" default:"2" range:"0..6" json:"octave,omitempty"`
	Mode   string            `cicada:"Voice mode" json:"mode"`
	Params []InstrumentParam `cicada:"Exposed synthesis parameters" json:"params"`
	Lets   []Binding         `cicada:"Named intermediate expressions" json:"lets"`
	Out    Expr              `cicada:"Instrument output expression" json:"out"`
}

type InstrumentParam struct {
	ID      string  `cicada:"Parameter identifier" json:"id"`
	Unit    string  `cicada:"Parameter measurement unit" json:"unit"`
	Default float64 `cicada:"Parameter initial value" json:"default"`
}

type Binding struct {
	ID    string `cicada:"Binding identifier" json:"id"`
	Value Expr   `cicada:"Expression assigned to the binding" json:"value"`
}

// Expr is encoded as exactly one of literal, name, or op+args.
type Expr struct {
	Op      string   `cicada:"Expression operator" variant:"operator" json:"op"`
	Args    []Expr   `cicada:"Operator arguments" variant:"operator" json:"args"`
	Literal *float64 `cicada:"Numeric literal" variant:"literal" json:"literal"`
	Name    string   `cicada:"Named expression reference" variant:"name" json:"name"`
}

type Kit struct {
	ID    string            `cicada:"Kit identifier" json:"id"`
	Lanes map[string]string `cicada:"Drum lane to instrument assignments" json:"lanes"`
}

type Track struct {
	ID     string           `cicada:"Track identifier" json:"id"`
	Kind   string           `cicada:"Track instrument kind" json:"kind"`
	Params map[string]Value `cicada:"Track instrument parameters" json:"params"`
	Mixer  Mixer            `cicada:"Track mixer state" json:"mixer"`
	Slots  [16]*string      `cicada:"Sixteen pattern slots" json:"slots"`
}

type Value struct {
	Unit   string   `cicada:"Value measurement unit" json:"unit"`
	Number *float64 `cicada:"Numeric value" variant:"number" json:"number"`
	Text   string   `cicada:"Enumerated text value" variant:"text" json:"text"`
}

type Mixer struct {
	GainDB  float64     `cicada:"Track gain in decibels" unit:"dB" range:"-60..6" json:"gain_db" legacy:"cicada.project/1"`
	Pan     float64     `cicada:"Stereo pan position" range:"-1..1" json:"pan"`
	SendA   float64     `cicada:"Send A gain" range:"0..1" json:"send_a" legacy:"cicada.project/1"`
	SendB   float64     `cicada:"Send B gain" range:"0..1" json:"send_b" legacy:"cicada.project/1"`
	SendPre bool        `cicada:"Pre fader send switch" json:"send_pre" legacy:"cicada.project/1"`
	Mute    bool        `cicada:"Mute switch" json:"mute"`
	Solo    bool        `cicada:"Solo switch" json:"solo"`
	Insert  string      `cicada:"Insert effect identifier" json:"insert" legacy:"cicada.project/1"`
	Bus     string      `cicada:"Output bus identifier" json:"bus" legacy:"cicada.project/1"`
	Level   *Value      `cicada:"Explicit mixer level" json:"level,omitempty" introduced:"cicada.project/2"`
	Inserts []string    `cicada:"Ordered insert chain" json:"inserts,omitempty" introduced:"cicada.project/2"`
	Sends   []MixerSend `cicada:"Ordered named sends" json:"sends,omitempty" introduced:"cicada.project/2"`
	Out     *string     `cicada:"Explicit output bus" json:"out,omitempty" introduced:"cicada.project/2"`
	panSet  bool
	muteSet bool
	soloSet bool
	wireV2  bool
}

type MixerSend struct {
	To    string `cicada:"Effect or bus destination" json:"to" introduced:"cicada.project/2"`
	Level Value  `cicada:"Send amount in linear gain or dB" json:"level" introduced:"cicada.project/2"`
	Tap   string `cicada:"Pre or post fader tap" json:"tap" introduced:"cicada.project/2"`
}

type Bus struct {
	ID    string `cicada:"Bus identifier" json:"id" introduced:"cicada.project/2"`
	Mixer Mixer  `cicada:"Bus mixer state" json:"mixer" introduced:"cicada.project/2"`
}

type Master struct {
	Mixer Mixer `cicada:"Master mixer state" json:"mixer" introduced:"cicada.project/2"`
}

type Export struct {
	ID        string `cicada:"Export identifier" json:"id" introduced:"cicada.project/2"`
	Rate      *int   `cicada:"Output sample rate" json:"rate,omitempty" introduced:"cicada.project/2"`
	Bits      *int   `cicada:"Output sample depth" json:"bits,omitempty" introduced:"cicada.project/2"`
	Tail      *Value `cicada:"Render tail length" json:"tail,omitempty" introduced:"cicada.project/2"`
	Loudness  *Value `cicada:"Integrated loudness target" json:"loudness,omitempty" introduced:"cicada.project/2"`
	TruePeak  *Value `cicada:"True peak target" json:"true_peak,omitempty" introduced:"cicada.project/2"`
	Normalize *bool  `cicada:"Static gain normalization switch" json:"normalize,omitempty" introduced:"cicada.project/2"`
}

type Pattern struct {
	ID              string             `cicada:"Pattern identifier" json:"id"`
	Kind            string             `cicada:"Melodic or drum pattern kind" json:"kind"`
	Steps           uint8              `cicada:"Number of steps" range:"1..64" json:"steps"`
	SwingPercent100 uint16             `cicada:"Swing percentage scaled by one hundred" unit:"percent/100" range:"5000..7500" json:"swing_percent100"`
	GatePercent     uint8              `cicada:"Note gate percentage" unit:"percent" range:"10..100" json:"gate_percent"`
	Transpose       int8               `cicada:"Semitone transposition" unit:"semitone" range:"-24..24" json:"transpose"`
	Seed            uint32             `cicada:"Pattern random seed" json:"seed"`
	Data            []*Step            `cicada:"Melodic steps" json:"data"`
	Lanes           map[string][]*Step `cicada:"Drum lane steps" json:"lanes"`
}

type Step struct {
	Note        uint8 `cicada:"MIDI note number" range:"0..127" json:"note"`
	Accent      bool  `cicada:"Accent switch" json:"accent"`
	Slide       bool  `cicada:"Slide switch" json:"slide"`
	Tie         bool  `cicada:"Tie switch" json:"tie"`
	Ratchet     uint8 `cicada:"Retrigger count" range:"1..8" json:"ratchet"`
	Probability uint8 `cicada:"Playback probability percentage" unit:"percent" range:"0..100" json:"probability"`
	Velocity    uint8 `cicada:"MIDI velocity" range:"0..127" json:"velocity"`
}

type Scene struct {
	ID       string            `cicada:"Scene identifier" json:"id"`
	Bindings map[string]string `cicada:"Track to pattern assignments" json:"bindings"`
	Settings []SceneSetting    `cicada:"Ordered live parameter settings" json:"settings,omitempty" introduced:"cicada.project/2"`
}

type SceneSetting struct {
	Path  string     `cicada:"Resolved parameter path" json:"path" introduced:"cicada.project/2"`
	Value SceneValue `cicada:"Parameter value in base units" json:"value" introduced:"cicada.project/2"`
}

// SceneValue is the /2 wire form of a path-addressed parameter value. Numeric
// values carry only their base-unit number and unit; enums retain text.
type SceneValue struct {
	Unit   string   `cicada:"Base measurement unit or enum" json:"unit,omitempty" variant:"unit" introduced:"cicada.project/2"`
	Number *float64 `cicada:"Numeric value in base units" json:"number,omitempty" variant:"number" introduced:"cicada.project/2"`
	Text   string   `cicada:"Enumerated value" json:"text,omitempty" variant:"text" introduced:"cicada.project/2"`
}

func sceneValueFrom(value Value) SceneValue {
	return SceneValue{Unit: value.Unit, Number: value.Number, Text: value.Text}
}

func (value SceneValue) projectValue() Value {
	return Value{Unit: value.Unit, Number: value.Number, Text: value.Text}
}

func (value *SceneValue) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*value = SceneValue{}
	var unit string
	if raw, ok := fields["unit"]; !ok || json.Unmarshal(raw, &unit) != nil || unit == "" {
		return fmt.Errorf("scene value needs a unit")
	}
	if unit == "enum" {
		if len(fields) != 2 {
			return fmt.Errorf("enum scene value must contain only unit and text")
		}
		raw, ok := fields["text"]
		if !ok || json.Unmarshal(raw, &value.Text) != nil || value.Text == "" {
			return fmt.Errorf("enum scene value needs text")
		}
		value.Unit = unit
		return nil
	}
	if len(fields) != 2 {
		return fmt.Errorf("numeric scene value must contain only unit and number")
	}
	raw, ok := fields["number"]
	if !ok || json.Unmarshal(raw, &value.Number) != nil || value.Number == nil {
		return fmt.Errorf("numeric scene value needs a number")
	}
	value.Unit = unit
	return nil
}

type SongEntry struct {
	Scene string `cicada:"Scene identifier" json:"scene"`
	Bars  uint16 `cicada:"Scene duration in bars" unit:"bar" range:"1..999" json:"bars"`
}

type Effect struct {
	ID     string           `cicada:"Effect identifier" json:"id"`
	Kind   string           `cicada:"Built-in effect kind" json:"kind" introduced:"cicada.project/2"`
	Params map[string]Value `cicada:"Effect parameters" json:"params"`
}

var laneOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}

func defaultMixer() Mixer { return Mixer{GainDB: -6, Insert: "none", Bus: "music"} }

func projectHasSceneSettings(p *Project) bool {
	if p == nil {
		return false
	}
	for _, scene := range p.Scenes {
		if len(scene.Settings) > 0 {
			return true
		}
	}
	return false
}

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
		Format: FormatID, Version: 1, Edition: score.Version, Title: score.Title, TempoMilli: int(score.TempoMilli),
		Key: Key{Root: root, Scale: score.Scale}, Seed: uint32(score.Seed),
		Instruments: []Instrument{}, Kits: []Kit{}, Tracks: []Track{}, Patterns: []Pattern{},
		Scenes: []Scene{}, Song: []SongEntry{}, Effects: []Effect{},
	}
	lowerAudio(p, score)
	for _, source := range score.Instruments {
		octave := source.Octave
		inst := Instrument{ID: source.Name, Octave: &octave, Mode: source.Mode, Params: []InstrumentParam{}, Lets: []Binding{}}
		for _, param := range source.Params {
			value, inferredUnit, err := parseBaseValue(param.Default)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-UNIT", Severity: "error", Message: err.Error(), Position: param.Position})
			}
			unit := param.Unit
			if unit == "" {
				unit = inferredUnit
			}
			inst.Params = append(inst.Params, InstrumentParam{ID: param.Name, Unit: unit, Default: value})
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
	needsProject2 := sourceUsesNamedMixer(score) || sourceHasAudio(score) || score.Live != nil
	p.Live = liveFromScore(score.Live)
	for _, scene := range score.Scenes {
		needsProject2 = needsProject2 || len(scene.Settings) > 0
	}
	effectKinds := make(map[string]string, len(score.Effects))
	for _, source := range score.Effects {
		effectKinds[source.Name] = source.Kind
		kind := source.Kind
		if kind == "" {
			kind = source.Name
		}
		effect := Effect{ID: source.Name, Kind: kind, Params: map[string]Value{}}
		for _, param := range source.Params {
			value, err := projectValue(param.Value)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
			}
			effect.Params[param.Name] = value
			if kind == "drive" {
				if _, err := DriveParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if kind == "delay" {
				if _, err := DelayParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if kind == "reverb" {
				if _, err := ReverbParamsFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			} else if kind == "comp" {
				if _, _, err := CompSpecFromValues(map[string]Value{param.Name: value}); err != nil {
					return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: param.ValuePosition})
				}
			}
		}
		if source.Legacy && !needsProject2 {
			effect.Kind = ""
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
		resolveNamedMixer(&track.Mixer, source, effectKinds)
		if needsProject2 {
			track.Mixer.wireV2 = true
			for _, param := range source.Params {
				if param.Name == "level" && param.Value != "off" {
					value, unit, parseErr := parseBaseValue(param.Value)
					if parseErr == nil && unit == "db" {
						track.Mixer.Level = &Value{Unit: "db", Number: &value}
					}
				}
			}
		} else {
			track.Mixer.Level, track.Mixer.Inserts, track.Mixer.Sends, track.Mixer.Out = nil, nil, nil, nil
			track.Mixer.panSet, track.Mixer.muteSet, track.Mixer.soloSet, track.Mixer.wireV2 = false, false, false, false
		}
		for _, param := range source.Params {
			if isMixerSourceParam(param.Name) {
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
	for _, source := range score.Buses {
		settings := notation.Track{Name: source.Name, Params: source.Params}
		mixer, err := CompileMixerParams(settings)
		if err != nil {
			return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Message: err.Error(), Severity: "error", Position: source.Position})
		}
		mixer.wireV2 = true
		mixer.Bus, mixer.Out = source.Name, nil
		if source.Name == "music" {
			mixer.GainDB = -3
		} else {
			mixer.GainDB = 0
		}
		p.Buses = append(p.Buses, Bus{ID: source.Name, Mixer: mixer})
	}
	if needsProject2 {
		for _, effect := range score.Effects {
			if !effect.Legacy || effect.Kind != "comp" {
				continue
			}
			musicIndex := -1
			for i := range p.Buses {
				if p.Buses[i].ID == "music" {
					musicIndex = i
					break
				}
			}
			if musicIndex < 0 {
				p.Buses = append(p.Buses, Bus{ID: "music", Mixer: Mixer{
					GainDB: -3, Insert: effect.Name, Bus: "music", Inserts: []string{effect.Name}, wireV2: true,
				}})
				continue
			}
			if len(p.Buses[musicIndex].Mixer.Inserts) == 0 {
				p.Buses[musicIndex].Mixer.Insert = effect.Name
				p.Buses[musicIndex].Mixer.Inserts = []string{effect.Name}
			}
		}
	}
	if score.HasMaster {
		settings := notation.Track{Name: "master", Params: score.Master}
		mixer, err := CompileMixerParams(settings)
		if err != nil {
			return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Message: err.Error(), Severity: "error", Position: notation.Position{Line: 1, Column: 1}})
		}
		mixer.wireV2 = true
		if mixer.Level == nil {
			mixer.GainDB = 0
		}
		p.Master = &Master{Mixer: mixer}
	}
	for _, source := range score.Exports {
		export, err := compileExport(source)
		if err != nil {
			return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-PARAM", Severity: "error", Message: err.Error(), Position: source.Position})
		}
		p.Exports = append(p.Exports, export)
	}
	if needsProject2 {
		p.p2Syntax = true
		p.Format, p.Version = FormatID2, 2
	}
	for _, source := range score.Patterns {
		track := representativeTrack(score, source)
		compiled, err := CompilePattern(score, source, track)
		if err != nil {
			return nil, append(diagnostics, patternCompileDiagnostic(err, source.Position))
		}
		pattern := Pattern{
			ID: source.Name, Kind: semanticPatternKind(score, source), Steps: compiled[0].Pattern.Len,
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
			pattern := binding.Pattern
			if pattern == "keep" {
				continue // An omitted action has the same musical meaning.
			}
			if pattern == "stop" && !scoreHasPattern(score, "stop") {
				pattern = "off" // canonical project-1 action; source spelling is stop
			}
			scene.Bindings[binding.Track] = pattern
		}
		for _, setting := range source.Settings {
			resolved, resolveErr := ResolveParameterPath(p, setting.Path)
			if resolveErr != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-REFERENCE", Severity: "error", Message: resolveErr.Error(), Position: setting.Position})
			}
			value, err := parseParameterValue(resolved.Descriptor, setting.Value)
			if err != nil {
				return nil, append(diagnostics, notation.Diagnostic{Code: "CICADA-UNIT", Severity: "error", Message: err.Error(), Position: setting.ValuePosition})
			}
			scene.Settings = append(scene.Settings, SceneSetting{Path: setting.Path, Value: sceneValueFrom(value)})
		}
		if len(scene.Settings) > 0 {
			p.p2Syntax = true
			p.Format, p.Version = FormatID2, 2
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
		return nil, append(diagnostics, notation.Diagnostic{Code: projectDiagnosticCode(err), Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	if _, err := canonicalProjectBytes(p); err != nil {
		code := projectDiagnosticCode(err)
		if errors.Is(err, errCanonicalJSONLimit) {
			code = "CICADA-LIMIT"
		}
		return nil, append(diagnostics, notation.Diagnostic{Code: code, Severity: "error", Message: err.Error(), Position: notation.Position{Line: 1, Column: 1}})
	}
	return p, diagnostics
}

func projectDiagnosticCode(err error) string {
	if err != nil {
		code, _, ok := strings.Cut(err.Error(), ":")
		if ok && strings.HasPrefix(code, "CICADA-") {
			return code
		}
	}
	return "CICADA-PARAM"
}

func semanticPatternKind(score *notation.Score, pattern notation.Pattern) string {
	if pattern.Kind != "notes" {
		return pattern.Kind
	}
	usedByAcid := false
	for _, scene := range score.Scenes {
		for _, binding := range scene.Bindings {
			if binding.Pattern != pattern.Name {
				continue
			}
			for _, track := range score.Tracks {
				if track.Name != binding.Track {
					continue
				}
				if track.Kind != "acid" {
					return "notes"
				}
				usedByAcid = true
			}
		}
	}
	if usedByAcid {
		return "acid"
	}
	return "notes"
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

func scoreHasPattern(score *notation.Score, name string) bool {
	for _, pattern := range score.Patterns {
		if pattern.Name == name {
			return true
		}
	}
	return false
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
		if p.Edition == 2 && track.Kind == "audio" {
			var err error
			track.Slots, err = ClipSlots(p, *track)
			if err != nil {
				return err
			}
			continue
		}
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
	places := 0
	for _, suffix := range []struct {
		name, unit string
		places     int
	}{{"lufs", "lufs", 0}, {"dbtp", "dbtp", 0}, {"khz", "hz", 3}, {"hz", "hz", 0}, {"ms", "ms", 0}, {"db", "db", 0}, {"lu", "lu", 0}, {"s", "ms", 3}, {"%", "unit", -2}} {
		if strings.HasSuffix(strings.ToLower(source), suffix.name) {
			unit, places = suffix.unit, suffix.places
			source = source[:len(source)-len(suffix.name)]
			break
		}
	}
	shifted, err := shiftDecimal(source, places)
	if err != nil {
		return 0, "", err
	}
	value, err := strconv.ParseFloat(shifted, 64)
	if err != nil {
		return 0, "", err
	}
	return value, unit, nil
}

// shiftDecimal moves a decimal point in the literal text, preserving exact
// base-unit conversion until the final float64 parse.
func shiftDecimal(source string, places int) (string, error) {
	sign := ""
	if strings.HasPrefix(source, "-") {
		sign, source = "-", source[1:]
	} else if strings.HasPrefix(source, "+") {
		sign, source = "+", source[1:]
	}
	point := strings.IndexByte(source, '.')
	if point < 0 {
		point = len(source)
	}
	digits := strings.ReplaceAll(source, ".", "")
	if digits == "" {
		return "", fmt.Errorf("invalid decimal value")
	}
	newPoint := point + places
	if newPoint <= 0 {
		digits = strings.Repeat("0", -newPoint) + digits
		digits = "0." + digits
	} else if newPoint >= len(digits) {
		digits += strings.Repeat("0", newPoint-len(digits))
	} else {
		digits = digits[:newPoint] + "." + digits[newPoint:]
	}
	return sign + digits, nil
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
