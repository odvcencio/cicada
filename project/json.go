package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MarshalJSON supplies the effect kind when a migrated in-memory /2 value
// still carries the /1 shorthand representation for a built-in effect.
func (p Project) MarshalJSON() ([]byte, error) {
	copy := p
	if p.Format == FormatID2 {
		copy.Effects = append([]Effect{}, p.Effects...)
		for i := range copy.Effects {
			if copy.Effects[i].Kind == "" {
				copy.Effects[i].Kind = semanticEffectKind(copy.Effects[i])
			}
		}
	}
	type wireProject Project
	return json.Marshal(wireProject(copy))
}

const maxJSONBytes = 2 << 20
const maxJSONDepth = 64

var errCanonicalJSONLimit = errors.New("canonical project JSON exceeds 2 MiB")

func (e Expr) MarshalJSON() ([]byte, error) {
	switch {
	case e.Literal != nil && e.Name == "" && e.Op == "" && len(e.Args) == 0:
		return json.Marshal(struct {
			Literal float64 `json:"literal"`
		}{*e.Literal})
	case e.Name != "" && e.Literal == nil && e.Op == "" && len(e.Args) == 0:
		return json.Marshal(struct {
			Name string `json:"name"`
		}{e.Name})
	case e.Op != "" && e.Literal == nil && e.Name == "" && e.Args != nil:
		return json.Marshal(struct {
			Op   string `json:"op"`
			Args []Expr `json:"args"`
		}{e.Op, e.Args})
	default:
		return nil, fmt.Errorf("expression must be exactly one of literal, name, or op with args")
	}
}

func (e *Expr) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("expression must be an object")
	}
	*e = Expr{}
	if literal, ok := fields["literal"]; ok && len(fields) == 1 {
		var number float64
		if err := json.Unmarshal(literal, &number); err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("invalid expression literal")
		}
		e.Literal = &number
		return nil
	}
	if name, ok := fields["name"]; ok && len(fields) == 1 {
		if err := json.Unmarshal(name, &e.Name); err != nil || e.Name == "" {
			return fmt.Errorf("invalid expression name")
		}
		return nil
	}
	if op, ok := fields["op"]; ok && len(fields) == 2 {
		args, hasArgs := fields["args"]
		if !hasArgs {
			return fmt.Errorf("expression operation needs args")
		}
		if err := json.Unmarshal(op, &e.Op); err != nil || e.Op == "" {
			return fmt.Errorf("invalid expression operation")
		}
		if err := json.Unmarshal(args, &e.Args); err != nil || e.Args == nil {
			return fmt.Errorf("invalid expression arguments")
		}
		return nil
	}
	return fmt.Errorf("expression must be exactly one of literal, name, or op with args")
}

func (e Effect) MarshalJSON() ([]byte, error) {
	if e.Kind == "" {
		return json.Marshal(struct {
			ID     string           `json:"id"`
			Params map[string]Value `json:"params"`
		}{e.ID, e.Params})
	}
	return json.Marshal(struct {
		ID     string           `json:"id"`
		Kind   string           `json:"kind"`
		Params map[string]Value `json:"params"`
	}{e.ID, e.Kind, e.Params})
}

func (e *Effect) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return fmt.Errorf("effect must be an object")
	}
	for key := range fields {
		if key != "id" && key != "kind" && key != "params" {
			return fmt.Errorf("effect has unknown field %s", key)
		}
	}
	*e = Effect{}
	if raw, ok := fields["id"]; !ok || json.Unmarshal(raw, &e.ID) != nil || e.ID == "" {
		return fmt.Errorf("effect needs an id")
	}
	if raw, ok := fields["kind"]; ok {
		if json.Unmarshal(raw, &e.Kind) != nil || e.Kind == "" {
			return fmt.Errorf("effect kind is invalid")
		}
	}
	if raw, ok := fields["params"]; !ok || json.Unmarshal(raw, &e.Params) != nil || e.Params == nil {
		return fmt.Errorf("effect needs a params object")
	}
	return nil
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.Number != nil && v.Unit != "enum" && v.Text == "" {
		return json.Marshal(struct {
			Unit   string  `json:"unit"`
			Number float64 `json:"number"`
		}{v.Unit, *v.Number})
	}
	if v.Number == nil && v.Unit == "enum" && v.Text != "" {
		return json.Marshal(struct {
			Unit string `json:"unit"`
			Text string `json:"text"`
		}{v.Unit, v.Text})
	}
	return nil, fmt.Errorf("value must have a number or enum text")
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil || len(fields) != 2 {
		return fmt.Errorf("value must have unit and one payload")
	}
	if err := json.Unmarshal(fields["unit"], &v.Unit); err != nil || v.Unit == "" {
		return fmt.Errorf("invalid value unit")
	}
	if number, ok := fields["number"]; ok && v.Unit != "enum" {
		var parsed float64
		if err := json.Unmarshal(number, &parsed); err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return fmt.Errorf("invalid numeric value")
		}
		v.Number, v.Text = &parsed, ""
		return nil
	}
	if text, ok := fields["text"]; ok && v.Unit == "enum" {
		if err := json.Unmarshal(text, &v.Text); err != nil || v.Text == "" {
			return fmt.Errorf("invalid enum value")
		}
		v.Number = nil
		return nil
	}
	return fmt.Errorf("value must have a number or enum text")
}

type mixerV1 struct {
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

func (m Mixer) MarshalJSON() ([]byte, error) {
	if !m.wireV2 {
		return json.Marshal(mixerV1{m.GainDB, m.Pan, m.SendA, m.SendB, m.SendPre, m.Mute, m.Solo, m.Insert, m.Bus})
	}
	fields := make(map[string]any)
	if m.Level != nil {
		fields["level"] = m.Level
	}
	if m.panSet {
		fields["pan"] = m.Pan
	}
	if m.muteSet {
		fields["mute"] = m.Mute
	}
	if m.soloSet {
		fields["solo"] = m.Solo
	}
	if m.Inserts != nil {
		fields["inserts"] = m.Inserts
	}
	if m.Sends != nil {
		fields["sends"] = m.Sends
	}
	if m.Out != nil {
		fields["out"] = *m.Out
	}
	return json.Marshal(fields)
}

func (m *Mixer) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return fmt.Errorf("mixer must be an object")
	}
	for key := range fields {
		if !strings.Contains("|gain_db|pan|send_a|send_b|send_pre|mute|solo|insert|bus|level|inserts|sends|out|", "|"+key+"|") {
			return fmt.Errorf("mixer has unknown field %s", key)
		}
	}
	*m = defaultMixer()
	if _, old := fields["gain_db"]; old {
		var legacy mixerV1
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		*m = Mixer{GainDB: legacy.GainDB, Pan: legacy.Pan, SendA: legacy.SendA, SendB: legacy.SendB,
			SendPre: legacy.SendPre, Mute: legacy.Mute, Solo: legacy.Solo, Insert: legacy.Insert, Bus: legacy.Bus}
		return nil
	}
	m.wireV2 = true
	if raw, ok := fields["level"]; ok {
		var value Value
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("mixer level: %w", err)
		}
		m.Level = &value
		if value.Unit == "db" && value.Number != nil {
			m.GainDB = *value.Number
		} else if value.Unit == "enum" && value.Text == "off" {
			m.Mute = true
		}
	}
	if raw, ok := fields["pan"]; ok {
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		m.panSet, m.Pan = true, value
	}
	if raw, ok := fields["mute"]; ok {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		m.muteSet, m.Mute = true, value
	}
	if raw, ok := fields["solo"]; ok {
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		m.soloSet, m.Solo = true, value
	}
	if raw, ok := fields["inserts"]; ok {
		if err := json.Unmarshal(raw, &m.Inserts); err != nil || m.Inserts == nil {
			return fmt.Errorf("mixer inserts must be an array")
		}
		m.Insert = "none"
		if len(m.Inserts) > 0 {
			m.Insert = m.Inserts[0]
		}
	}
	if raw, ok := fields["sends"]; ok {
		if err := json.Unmarshal(raw, &m.Sends); err != nil || m.Sends == nil {
			return fmt.Errorf("mixer sends must be an array")
		}
	}
	if raw, ok := fields["out"]; ok {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			return fmt.Errorf("mixer output bus is invalid")
		}
		m.Out, m.Bus = &value, value
	}
	return nil
}

// CanonicalJSON writes sorted object keys, two-space indentation, LF, and a
// final LF. It expands exponent-form JSON numbers to decimal notation.
func CanonicalJSON(p *Project) ([]byte, error) {
	if err := ValidateProject(p); err != nil {
		return nil, err
	}
	encoded, err := canonicalProjectBytes(p)
	if err != nil {
		return nil, err
	}
	if p.Format == FormatID && p.Version == 1 {
		if _, err := ToSource(p); err != nil {
			return nil, fmt.Errorf("project cannot be represented by source v1: %w", err)
		}
	}
	return encoded, nil
}

// canonicalProjectBytes also runs during source lowering. Every project that
// validates must fit the same canonical body that DecodeJSON can accept.
func canonicalProjectBytes(p *Project) ([]byte, error) {
	// Project-1 accepts explicit acid/note kinds and keep actions for older
	// data. Canonical output drops distinctions that the current track and scene
	// already imply, matching headerless source after a round trip.
	normalized := *p
	normalized.Format, normalized.Version = FormatID, 1
	useV2 := len(p.Automation) > 0 || p.HasAudio() || p.Live != nil || p.p2Syntax || projectHasSceneSettings(p) || len(p.Buses) > 0 || p.Master != nil || len(p.Exports) > 0
	for _, effect := range p.Effects {
		useV2 = useV2 || effect.Kind != ""
	}
	if useV2 {
		normalized.Format, normalized.Version = FormatID2, 2
	}
	normalized.Tracks = append([]Track(nil), p.Tracks...)
	for i := range normalized.Tracks {
		normalized.Tracks[i].Mixer.wireV2 = useV2
	}
	normalized.Buses = append([]Bus(nil), p.Buses...)
	for i := range normalized.Buses {
		normalized.Buses[i].Mixer.wireV2 = true
	}
	if normalized.Master != nil {
		master := *normalized.Master
		master.Mixer.wireV2 = true
		normalized.Master = &master
	}
	normalized.Effects = append([]Effect{}, p.Effects...)
	for i := range normalized.Effects {
		if useV2 && normalized.Effects[i].Kind == "" {
			normalized.Effects[i].Kind = normalized.Effects[i].ID
		}
		if !useV2 {
			normalized.Effects[i].Kind = ""
		}
	}
	normalized.Patterns = append([]Pattern(nil), p.Patterns...)
	for i := range normalized.Patterns {
		if normalized.Patterns[i].Kind == "notes" && projectPatternUsedOnlyByAcid(p, normalized.Patterns[i].ID) {
			normalized.Patterns[i].Kind = "acid"
		}
	}
	normalized.Scenes = append([]Scene(nil), p.Scenes...)
	for i := range normalized.Scenes {
		bindings := make(map[string]string, len(p.Scenes[i].Bindings))
		for track, pattern := range p.Scenes[i].Bindings {
			if pattern != "keep" {
				bindings[track] = pattern
			}
		}
		normalized.Scenes[i].Bindings = bindings
	}
	raw, err := json.Marshal(&normalized)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCanonical(&output, value, 0); err != nil {
		return nil, err
	}
	output.WriteByte('\n')
	if output.Len() > maxJSONBytes {
		return nil, errCanonicalJSONLimit
	}
	return output.Bytes(), nil
}

func projectPatternUsedOnlyByAcid(p *Project, patternID string) bool {
	used := false
	for _, scene := range p.Scenes {
		for trackID, assigned := range scene.Bindings {
			if assigned != patternID {
				continue
			}
			for _, track := range p.Tracks {
				if track.ID != trackID {
					continue
				}
				if track.Kind != "acid" {
					return false
				}
				used = true
			}
		}
	}
	return used
}

func writeCanonical(output *bytes.Buffer, value any, depth int) error {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			output.WriteString("{}")
			return nil
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteString("{\n")
		for i, key := range keys {
			output.WriteString(strings.Repeat("  ", depth+1))
			encoded, _ := json.Marshal(key)
			output.Write(encoded)
			output.WriteString(": ")
			if err := writeCanonical(output, v[key], depth+1); err != nil {
				return err
			}
			if i+1 < len(keys) {
				output.WriteByte(',')
			}
			output.WriteByte('\n')
		}
		output.WriteString(strings.Repeat("  ", depth))
		output.WriteByte('}')
	case []any:
		if len(v) == 0 {
			output.WriteString("[]")
			return nil
		}
		output.WriteString("[\n")
		for i, item := range v {
			output.WriteString(strings.Repeat("  ", depth+1))
			if err := writeCanonical(output, item, depth+1); err != nil {
				return err
			}
			if i+1 < len(v) {
				output.WriteByte(',')
			}
			output.WriteByte('\n')
		}
		output.WriteString(strings.Repeat("  ", depth))
		output.WriteByte(']')
	case json.Number:
		parsed, err := strconv.ParseFloat(string(v), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return fmt.Errorf("nonfinite JSON number")
		}
		if parsed == 0 {
			output.WriteByte('0')
		} else {
			output.WriteString(strconv.FormatFloat(parsed, 'f', -1, 64))
		}
	case string, bool, nil:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		output.Write(encoded)
	default:
		return fmt.Errorf("unsupported JSON value %T", v)
	}
	return nil
}

// DecodeJSON rejects malformed interchange before the project compiler sees
// it. Structural and musical cross-reference checks are separate stages.
func DecodeJSON(data []byte) (*Project, error) {
	if len(data) > maxJSONBytes {
		return nil, jsonError(data, "CICADA-LIMIT", "", 0, fmt.Errorf("project JSON exceeds 2 MiB"))
	}
	if !utf8.Valid(data) {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, fmt.Errorf("project JSON is not UTF-8"))
	}
	if err := checkJSONStructure(data); err != nil {
		return nil, err
	}
	if err := checkRequiredFields(data); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var p Project
	if err := decoder.Decode(&p); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, err)
	}
	effectKinds := make(map[string]string, len(p.Effects))
	for _, effect := range p.Effects {
		effectKinds[effect.ID] = semanticEffectKind(effect)
	}
	for i := range p.Tracks {
		for _, send := range p.Tracks[i].Mixer.Sends {
			if send.Level.Number == nil {
				continue
			}
			gain := *send.Level.Number
			if send.Level.Unit == "db" {
				gain = math.Pow(10, gain/20)
			}
			switch effectKinds[send.To] {
			case "delay":
				p.Tracks[i].Mixer.SendA = gain
			case "reverb":
				p.Tracks[i].Mixer.SendB = gain
			}
		}
	}
	for i := range p.Buses {
		bus := &p.Buses[i]
		bus.Mixer.Bus = bus.ID
		if bus.Mixer.Level == nil || bus.Mixer.Level.Unit == "enum" && bus.Mixer.Level.Text == "off" {
			if bus.ID == "music" {
				bus.Mixer.GainDB = -3
			} else if bus.ID == "sfx" {
				bus.Mixer.GainDB = 0
			}
		}
	}
	if p.Master != nil && (p.Master.Mixer.Level == nil || p.Master.Mixer.Level.Unit == "enum" && p.Master.Mixer.Level.Text == "off") {
		p.Master.Mixer.GainDB = 0
	}
	if !((p.Format == FormatID && p.Version == 1) || (p.Format == FormatID2 && p.Version == 2)) {
		field := "format"
		if p.Format == FormatID || p.Format == FormatID2 {
			field = "version"
		}
		return nil, jsonError(data, "CICADA-VERSION", "/"+field, 0, fmt.Errorf("unsupported project format or version"))
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, err)
	}
	p.p2Syntax = p.Format == FormatID2 && (p.Live != nil || projectHasSceneSettings(&p) || len(p.Buses) > 0 || p.Master != nil || len(p.Exports) > 0)
	for _, effect := range p.Effects {
		p.p2Syntax = p.p2Syntax || effect.Kind != ""
	}
	for _, track := range p.Tracks {
		p.p2Syntax = p.p2Syntax || track.Mixer.wireV2 && (len(track.Mixer.Sends) > 0 || track.Mixer.Out != nil || track.Mixer.Level != nil || track.Mixer.Inserts != nil || track.Mixer.soloSet || track.Mixer.muteSet || track.Mixer.panSet)
	}
	if _, present := root["edition"]; !present {
		// Semantic JSON written before editions existed means edition 1.
		p.Edition = 1
	}
	var rawInstruments []map[string]json.RawMessage
	if err := json.Unmarshal(root["instruments"], &rawInstruments); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "/instruments", 0, err)
	}
	for i := range p.Instruments {
		if raw, present := rawInstruments[i]["octave"]; present {
			if bytes.Equal(raw, []byte("null")) {
				return nil, jsonError(data, "CICADA-PARAM", fmt.Sprintf("/instruments/%d/octave", i), 0, fmt.Errorf("octave cannot be null"))
			}
		} else {
			legacyOctave := 2
			p.Instruments[i].Octave = &legacyOctave
		}
	}
	if p.Edition != 1 && p.Edition != 2 {
		return nil, jsonError(data, "CICADA-VERSION", "/edition", 0, fmt.Errorf("only cicada 1 and 2 are supported"))
	}
	if p.Format == FormatID && projectHasSceneSettings(&p) {
		return nil, jsonError(data, "CICADA-VERSION", "/format", 0, fmt.Errorf("scene settings require cicada.project/2"))
	}
	if err := ValidateProject(&p); err != nil {
		return nil, jsonError(data, projectDiagnosticCode(err), "", 0, err)
	}
	if _, err := canonicalProjectBytes(&p); err != nil {
		code := "CICADA-PARAM"
		if errors.Is(err, errCanonicalJSONLimit) {
			code = "CICADA-LIMIT"
		}
		return nil, jsonError(data, code, "", 0, err)
	}
	if p.Format == FormatID && p.Version == 1 {
		if _, err := ToSource(&p); err != nil {
			return nil, jsonError(data, "CICADA-PARAM", "", 0, fmt.Errorf("project cannot be represented by source v1: %w", err))
		}
	}
	return &p, nil
}

func checkJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(int, string) error
	walk = func(depth int, pointer string) error {
		if depth > maxJSONDepth {
			return jsonError(data, "CICADA-LIMIT", pointer, int(decoder.InputOffset()), fmt.Errorf("project JSON exceeds depth 64"))
		}
		token, err := decoder.Token()
		if err != nil {
			return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), fmt.Errorf("invalid JSON object key"))
				}
				child := jsonPointer(pointer, key)
				if seen[key] {
					return jsonError(data, "CICADA-DUPLICATE", child, jsonKeyOffset(data, int(decoder.InputOffset())), fmt.Errorf("duplicate JSON key %q", key))
				}
				seen[key] = true
				if err := walk(depth+1, child); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			if err != nil {
				return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), err)
			}
			return nil
		case '[':
			index := 0
			for decoder.More() {
				if err := walk(depth+1, jsonPointer(pointer, strconv.Itoa(index))); err != nil {
					return err
				}
				index++
			}
			_, err := decoder.Token()
			if err != nil {
				return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), err)
			}
			return nil
		}
		return jsonError(data, "CICADA-SYNTAX", pointer, int(decoder.InputOffset()), fmt.Errorf("unexpected JSON delimiter"))
	}
	if err := walk(0, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return jsonError(data, "CICADA-SYNTAX", "", int(decoder.InputOffset()), fmt.Errorf("extra JSON value"))
		}
		return jsonError(data, "CICADA-SYNTAX", "", int(decoder.InputOffset()), err)
	}
	return nil
}

func checkRequiredFields(data []byte) error {
	catalog, err := Fields()
	if err != nil {
		return err
	}
	fieldsByConstruct := make(map[string][]string, len(catalog.Constructs))
	optionalByConstruct := make(map[string][]string)
	for _, field := range catalog.Fields {
		if field.Variant != "" {
			continue
		}
		if field.Required {
			fieldsByConstruct[field.Construct] = append(fieldsByConstruct[field.Construct], field.Name)
		} else {
			optionalByConstruct[field.Construct] = append(optionalByConstruct[field.Construct], field.Name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	rootObject, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("project must be an object")
	}
	version2 := rootObject["format"] == FormatID2
	if version2 {
		fieldsByConstruct["mixer"] = []string{}
		optionalByConstruct["mixer"] = []string{"level", "pan", "mute", "solo", "inserts", "sends", "out"}
		fieldsByConstruct["effect"] = []string{"id", "kind", "params"}
		optionalByConstruct["effect"] = nil
	} else {
		fieldsByConstruct["mixer"] = []string{"gain_db", "pan", "send_a", "send_b", "send_pre", "mute", "solo", "insert", "bus"}
		optionalByConstruct["mixer"] = nil
		fieldsByConstruct["effect"] = []string{"id", "params"}
		optionalByConstruct["effect"] = nil
		optionalByConstruct["project"] = removeNames(optionalByConstruct["project"], "buses", "master", "exports", "assets", "clips", "samplers", "live")
	}
	require := func(value any, construct, name, pointer string) (map[string]any, error) {
		fields, ok := fieldsByConstruct[construct]
		if !ok || len(fields) == 0 && construct != "mixer" {
			return nil, fmt.Errorf("no required fields registered for %s", construct)
		}
		return requiredObject(value, name, pointer, fields, optionalByConstruct[construct])
	}
	root, err := require(raw, "project", "project", "")
	if err != nil {
		return err
	}
	for _, construct := range []struct{ array, name string }{{"assets", "asset"}, {"clips", "clip"}, {"samplers", "sampler"}} {
		if value, exists := root[construct.array]; exists {
			if err := checkObjectArray(value, construct.array, "/"+construct.array, func(item any, pointer string) error {
				_, err := require(item, construct.name, construct.name, pointer)
				return err
			}); err != nil {
				return err
			}
		}
	}
	if _, err := require(root["key"], "key", "key", "/key"); err != nil {
		return err
	}
	if err := checkObjectArray(root["instruments"], "instruments", "/instruments", func(value any, pointer string) error {
		object, err := require(value, "instrument", "instrument", pointer)
		if err != nil {
			return err
		}
		if err := checkObjectArray(object["params"], "instrument params", pointer+"/params", func(value any, child string) error {
			_, err := require(value, "instrument_param", "instrument param", child)
			return err
		}); err != nil {
			return err
		}
		return checkObjectArray(object["lets"], "instrument lets", pointer+"/lets", func(value any, child string) error {
			_, err := require(value, "binding", "binding", child)
			return err
		})
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["kits"], "kits", "/kits", func(value any, pointer string) error {
		_, err := require(value, "kit", "kit", pointer)
		return err
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["tracks"], "tracks", "/tracks", func(value any, pointer string) error {
		object, err := require(value, "track", "track", pointer)
		if err != nil {
			return err
		}
		mixer, err := require(object["mixer"], "mixer", "mixer", pointer+"/mixer")
		if err != nil {
			return err
		}
		if version2 {
			return checkObjectArrayOptional(mixer["sends"], "mixer sends", pointer+"/mixer/sends", func(value any, child string) error {
				_, err := require(value, "mixer_send", "mixer send", child)
				return err
			})
		}
		return nil
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["patterns"], "patterns", "/patterns", func(value any, pointer string) error {
		object, err := require(value, "pattern", "pattern", pointer)
		if err != nil {
			return err
		}
		if err := checkStepArray(object["data"], pointer+"/data", fieldsByConstruct["step"]); err != nil {
			return err
		}
		if expression, present := object["expression"]; present {
			if err := checkObjectArray(expression, "expression", pointer+"/expression", func(value any, child string) error {
				_, err := require(value, "note_expression", "note expression", child)
				return err
			}); err != nil {
				return err
			}
		}
		lanes, ok := object["lanes"].(map[string]any)
		if !ok {
			return &jsonFieldError{pointer + "/lanes", fmt.Errorf("pattern lanes must be an object")}
		}
		for _, lane := range sortedKeys(lanes) {
			if err := checkStepArray(lanes[lane], jsonPointer(pointer+"/lanes", lane), fieldsByConstruct["step"]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["scenes"], "scenes", "/scenes", func(value any, pointer string) error {
		object, err := require(value, "scene", "scene", pointer)
		if err != nil {
			return err
		}
		settings, present := object["settings"]
		if !present {
			return nil
		}
		return checkObjectArray(settings, "scene settings", pointer+"/settings", func(value any, child string) error {
			_, err := require(value, "scene_setting", "scene setting", child)
			return err
		})
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["song"], "song", "/song", func(value any, pointer string) error {
		_, err := require(value, "song_entry", "song entry", pointer)
		return err
	}); err != nil {
		return err
	}
	if version2 {
		if rawLive, exists := root["live"]; exists {
			live, err := require(rawLive, "live", "live", "/live")
			if err != nil {
				return err
			}
			if err := checkObjectArray(live["macros"], "macros", "/live/macros", func(value any, pointer string) error {
				_, err := require(value, "live_macro", "macro", pointer)
				return err
			}); err != nil {
				return err
			}
			if err := checkObjectArray(live["layers"], "layers", "/live/layers", func(value any, pointer string) error {
				layers, err := require(value, "live_layers", "layers", pointer)
				if err != nil {
					return err
				}
				return checkObjectArray(layers["rules"], "layer rules", pointer+"/rules", func(value any, pointer string) error {
					_, err := require(value, "live_layer", "layer rule", pointer)
					return err
				})
			}); err != nil {
				return err
			}
		}
		if rawBuses, exists := root["buses"]; exists {
			if err := checkObjectArray(rawBuses, "buses", "/buses", func(value any, pointer string) error {
				object, err := require(value, "bus", "bus", pointer)
				if err != nil {
					return err
				}
				_, err = require(object["mixer"], "mixer", "mixer", pointer+"/mixer")
				return err
			}); err != nil {
				return err
			}
		}
		if rawMaster, exists := root["master"]; exists {
			object, err := require(rawMaster, "master", "master", "/master")
			if err != nil {
				return err
			}
			if _, err := require(object["mixer"], "mixer", "mixer", "/master/mixer"); err != nil {
				return err
			}
		}
		if rawExports, exists := root["exports"]; exists {
			if err := checkObjectArray(rawExports, "exports", "/exports", func(value any, pointer string) error {
				_, err := require(value, "export", "export", pointer)
				return err
			}); err != nil {
				return err
			}
		}
	}
	return checkObjectArray(root["effects"], "effects", "/effects", func(value any, pointer string) error {
		_, err := require(value, "effect", "effect", pointer)
		return err
	})
}

func removeNames(source []string, removed ...string) []string {
	var output []string
	for _, name := range source {
		keep := true
		for _, candidate := range removed {
			if name == candidate {
				keep = false
				break
			}
		}
		if keep {
			output = append(output, name)
		}
	}
	return output
}

func checkObjectArrayOptional(value any, name, pointer string, check func(any, string) error) error {
	if value == nil {
		return nil
	}
	return checkObjectArray(value, name, pointer, check)
}

func requiredObject(value any, name, pointer string, fields, optional []string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, &jsonFieldError{pointer, fmt.Errorf("%s must be an object", name)}
	}
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
		if _, exists := object[field]; !exists {
			return nil, &jsonFieldError{jsonPointer(pointer, field), fmt.Errorf("%s is missing %s", name, field)}
		}
	}
	for _, field := range optional {
		allowed[field] = true
	}
	for _, field := range sortedKeys(object) {
		if !allowed[field] {
			return nil, &jsonFieldError{jsonPointer(pointer, field), fmt.Errorf("%s has unknown field %s", name, field)}
		}
	}
	return object, nil
}

func checkObjectArray(value any, name, pointer string, check func(any, string) error) error {
	array, ok := value.([]any)
	if !ok {
		return &jsonFieldError{pointer, fmt.Errorf("%s must be an array", name)}
	}
	for index, item := range array {
		if err := check(item, jsonPointer(pointer, strconv.Itoa(index))); err != nil {
			return err
		}
	}
	return nil
}

func checkStepArray(value any, pointer string, fields []string) error {
	return checkObjectArray(value, "pattern cells", pointer, func(value any, child string) error {
		if value == nil {
			return nil
		}
		object, err := requiredObject(value, "step", child, fields, []string{"notes"})
		if err != nil {
			return err
		}
		if value, exists := object["notes"]; exists {
			notes, ok := value.([]any)
			if !ok || len(notes) < 2 || len(notes) > 4 {
				return &jsonFieldError{child + "/notes", fmt.Errorf("chord notes must be a 2 to 4 pitch array")}
			}
		}
		return nil
	})
}
