package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/project"
)

// mcpOutputSchema is a tool's output schema, built from the Go types of the
// documents it returns, so it names every field the result can hold and
// cannot fall behind them: each field's JSON name and type, nested objects
// and arrays, and the descriptions in notes, keyed by the field's dotted path
// in the document (scores.diagnostics.start.line), which give units and
// meanings the names do not. Each document's schema field is a constant, and
// a refusal (error, with code and message) fits every one. With several
// documents the result is any of them.
func mcpOutputSchema(notes map[string]string, documents ...any) json.RawMessage {
	var branches []map[string]any
	for _, document := range documents {
		value := reflect.ValueOf(document)
		root := schemaOfType(value.Type(), "", notes, map[reflect.Type]bool{})
		properties := root["properties"].(map[string]any)
		properties["schema"] = map[string]any{"const": value.FieldByName("Schema").String()}
		if _, ok := properties["error"]; !ok {
			properties["error"] = schemaOfType(reflect.TypeOf(jsonError{}), "error", notes, map[reflect.Type]bool{})
		}
		root["required"] = []string{"schema"}
		branches = append(branches, root)
	}
	schema := branches[0]
	if len(branches) > 1 {
		schema = map[string]any{"type": "object", "anyOf": branches}
	}
	data, _ := json.Marshal(schema)
	return data
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

// schemaOfType is the schema of one Go type at path. A field without
// omitempty whose Go value can be nil (a pointer, slice or map) may be null.
// A type that contains itself is an object below its first level.
func schemaOfType(t reflect.Type, path string, notes map[string]string, open map[reflect.Type]bool) map[string]any {
	schema := map[string]any{}
	switch {
	case t == rawMessageType || t.Kind() == reflect.Interface:
		// Any JSON value: the compiled meaning of a score, an example intent.
	case t.Kind() == reflect.Pointer:
		return schemaOfType(t.Elem(), path, notes, open)
	case t.Kind() == reflect.String:
		schema["type"] = "string"
	case t.Kind() == reflect.Bool:
		schema["type"] = "boolean"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		schema["type"] = "integer"
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		schema["type"] = "number"
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		schema["type"] = "array"
		schema["items"] = schemaOfType(t.Elem(), path, notes, open)
	case t.Kind() == reflect.Map:
		schema["type"] = "object"
	case t.Kind() == reflect.Struct && open[t]:
		schema["type"] = "object"
	case t.Kind() == reflect.Struct:
		open[t] = true
		properties := map[string]any{}
		addFields(t, path, notes, open, properties)
		delete(open, t)
		schema["type"], schema["properties"], schema["additionalProperties"] = "object", properties, false
	}
	if note := notes[path]; note != "" {
		schema["description"] = note
	}
	return schema
}

// addFields adds the JSON fields of struct type t, and of the structs it
// embeds, to properties.
func addFields(t reflect.Type, path string, notes map[string]string, open map[reflect.Type]bool, properties map[string]any) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		name, options, _ := strings.Cut(tag, ",")
		if tag == "-" || !field.IsExported() && !field.Anonymous {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				addFields(embedded, path, notes, open, properties)
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		at := name
		if path != "" {
			at = path + "." + name
		}
		schema := schemaOfType(field.Type, at, notes, open)
		kind := field.Type.Kind()
		if !strings.Contains(options, "omitempty") && (kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Map) && field.Type != rawMessageType {
			if kind, ok := schema["type"].(string); ok {
				schema["type"] = []string{kind, "null"}
			}
		}
		properties[name] = schema
	}
}

// The notes of each tool's documents: units, ranges and meanings that the
// field names do not carry.
var (
	mcpPositionNotes = map[string]string{
		"line": "from 1", "column": "from 1, in Unicode characters", "offset": "bytes from the start of the file, from 0",
	}
	mcpCheckNotes = withPositions(map[string]string{
		"errors":                                 "error diagnostics across the scores",
		"scores.revision":                        "SHA-256 of the score's bytes in hex; the revision cicada apply checks",
		"scores.diagnostics.severity":            "error or warning",
		"scores.diagnostics.hint":                "advice in words",
		"scores.diagnostics.fix":                 "a repair proven on the score at fix.revision",
		"scores.browser_profile.image_bytes":     "bytes",
		"scores.browser_profile.max_image_bytes": "bytes",
		"scores.packs.velocity_law":              "recorded or velocity/127",
	}, "scores.diagnostics.start", "scores.diagnostics.end", "scores.diagnostics.related")
	mcpSongNotes = map[string]string{
		"revision":        "SHA-256 of the score's bytes in hex",
		"scores.revision": "SHA-256 of the score's bytes in hex",
		"symbols.line":    "from 1",
		"symbols.column":  "from 1",
		"structure":       "the compiled meaning as semantic JSON",
	}
	mcpIntentsNotes = map[string]string{
		"envelope_version":      "the version field of an apply envelope",
		"intents.fields.unit":   "the unit of a number, such as dB or Hz",
		"intents.fields.values": "the values the field takes",
		"intents.reasons":       "reason codes of this intent's refusals",
	}
	mcpExplainNotes = map[string]string{
		"diagnostics.severities": "error, warning or both",
		"diagnostics.scopes":     "where the code is reported: score, command, studio or edit",
		"unit":                   "the unit of the field's value",
		"fields.unit":            "the unit of the field's value",
	}
	mcpLibraryNotes = map[string]string{
		"library.root":   "where the library resolves, relative to the root; empty for the user library",
		"library.sha256": "SHA-256 of the library's content",
	}
)

// withPositions adds the position notes under each prefix.
func withPositions(notes map[string]string, prefixes ...string) map[string]string {
	for _, prefix := range prefixes {
		for field, note := range mcpPositionNotes {
			notes[prefix+"."+field] = note
		}
	}
	return notes
}

// The documents each tool returns, for their output schemas.
type (
	mcpExplainOperation struct {
		Schema string `json:"schema"`
		instrument.OperationInfo
	}
	mcpExplainField struct {
		Schema string `json:"schema"`
		project.Field
	}
	mcpExplainConstruct struct {
		Schema    string                  `json:"schema"`
		Construct project.ConstructRecord `json:"construct"`
		Fields    []project.Field         `json:"fields"`
	}
)

// paramRegistryEntrySchema is one entry of the parameter registry, which
// project/params.json holds as data rather than as a Go type.
var paramRegistryEntrySchema = map[string]any{
	"type": "object", "additionalProperties": false,
	"properties": map[string]any{
		"id":           map[string]any{"type": "string"},
		"path":         map[string]any{"type": "string", "description": "the parameter's path after param:<track>. or param:"},
		"scope":        map[string]any{"type": "string", "description": "track, global, bus or master"},
		"voices":       map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}, "description": "the voices it applies to; null for every voice"},
		"source":       map[string]any{"type": "string", "description": "the setting's name in the score"},
		"unit":         map[string]any{"type": "string", "description": "the unit of min, max, default and display_step, such as dB or Hz; empty for a plain number"},
		"min":          map[string]any{"type": "number"},
		"max":          map[string]any{"type": "number"},
		"default":      map[string]any{"type": "number"},
		"curve":        map[string]any{"type": "string", "description": "how a control moves between min and max: linear, log, fader, toggle or enum"},
		"display_step": map[string]any{"type": "number"},
		"smoothing_ms": map[string]any{"type": "integer", "description": "milliseconds a live change takes"},
		"live":         map[string]any{"type": "boolean", "description": "it can change while playing"},
		"automatable":  map[string]any{"type": "boolean"},
		"persist":      map[string]any{"type": "string"},
		"type":         map[string]any{"type": "string"},
		"values":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "the values of an enum"},
		"off":          map[string]any{"type": "boolean", "description": "the minimum turns it off"},
		"schema":       map[string]any{"type": "string"},
	},
}

// mcpParamsSchema is the params tool's output schema.
func mcpParamsSchema() json.RawMessage {
	refusal, _ := json.Marshal(schemaOfType(reflect.TypeOf(jsonError{}), "error", nil, map[reflect.Type]bool{}))
	data, _ := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"schema"},
		"properties": map[string]any{
			"schema": map[string]any{"const": paramRegistrySchema},
			"params": map[string]any{"type": "array", "items": paramRegistryEntrySchema},
			"error":  json.RawMessage(refusal),
		},
	})
	return data
}

// The output schemas of the song, check, intents, explain and library tools.

func mcpSongSchema() json.RawMessage {
	return mcpOutputSchema(mcpSongNotes, songDocument{Schema: songSchema})
}

func mcpCheckSchema() json.RawMessage {
	return mcpOutputSchema(mcpCheckNotes, checkDocument{Schema: checkSchema})
}

func mcpIntentsSchema() json.RawMessage {
	return mcpOutputSchema(mcpIntentsNotes, intentsIndexDocument{Schema: intentsIndexSchema}, intentsDocument{Schema: intentsSchema})
}

func mcpExplainSchema() json.RawMessage {
	return mcpOutputSchema(mcpExplainNotes, diagnosticsDocument{Schema: diagnosticsSchema}, mcpExplainOperation{Schema: explainSchema}, mcpExplainField{Schema: explainSchema}, mcpExplainConstruct{Schema: explainSchema})
}

func mcpLibrarySchema() json.RawMessage {
	return mcpOutputSchema(mcpLibraryNotes, libraryDocument{Schema: librarySchema})
}
