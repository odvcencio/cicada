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
	if _, err := ToSource(p); err != nil {
		return nil, fmt.Errorf("project cannot be represented by source v1: %w", err)
	}
	return encoded, nil
}

// canonicalProjectBytes also runs during source lowering. Every project that
// validates must fit the same canonical body that DecodeJSON can accept.
func canonicalProjectBytes(p *Project) ([]byte, error) {
	raw, err := json.Marshal(p)
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
	if p.Format != FormatID || p.Version != 1 {
		field := "format"
		if p.Format == FormatID {
			field = "version"
		}
		return nil, jsonError(data, "CICADA-VERSION", "/"+field, 0, fmt.Errorf("unsupported project format or version"))
	}
	if err := ValidateProject(&p); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, err)
	}
	if _, err := canonicalProjectBytes(&p); err != nil {
		code := "CICADA-PARAM"
		if errors.Is(err, errCanonicalJSONLimit) {
			code = "CICADA-LIMIT"
		}
		return nil, jsonError(data, code, "", 0, err)
	}
	if _, err := ToSource(&p); err != nil {
		return nil, jsonError(data, "CICADA-PARAM", "", 0, fmt.Errorf("project cannot be represented by source v1: %w", err))
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
	for _, field := range catalog.Fields {
		if field.Required && field.Variant == "" {
			fieldsByConstruct[field.Construct] = append(fieldsByConstruct[field.Construct], field.Name)
		}
	}
	require := func(value any, construct, name, pointer string) (map[string]any, error) {
		fields, ok := fieldsByConstruct[construct]
		if !ok || len(fields) == 0 {
			return nil, fmt.Errorf("no required fields registered for %s", construct)
		}
		return requiredObject(value, name, pointer, fields...)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	root, err := require(raw, "project", "project", "")
	if err != nil {
		return err
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
		_, err = require(object["mixer"], "mixer", "mixer", pointer+"/mixer")
		return err
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
		_, err := require(value, "scene", "scene", pointer)
		return err
	}); err != nil {
		return err
	}
	if err := checkObjectArray(root["song"], "song", "/song", func(value any, pointer string) error {
		_, err := require(value, "song_entry", "song entry", pointer)
		return err
	}); err != nil {
		return err
	}
	return checkObjectArray(root["effects"], "effects", "/effects", func(value any, pointer string) error {
		_, err := require(value, "effect", "effect", pointer)
		return err
	})
}

func requiredObject(value any, name, pointer string, fields ...string) (map[string]any, error) {
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
		_, err := requiredObject(value, "step", child, fields...)
		return err
	})
}
