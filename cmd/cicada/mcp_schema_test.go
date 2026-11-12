package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
)

// validateSchema checks value against the subset of JSON Schema the tools'
// output schemas use (type, const, properties, additionalProperties false,
// required, items, anyOf) and returns each mismatch, with its path.
func validateSchema(schema map[string]any, value any, at string) []string {
	if branches, ok := schema["anyOf"].([]any); ok {
		var first []string
		for i, branch := range branches {
			problems := validateSchema(branch.(map[string]any), value, at)
			if len(problems) == 0 {
				return nil
			}
			if i == 0 {
				first = problems
			}
		}
		return append([]string{at + ": matches no branch of anyOf; the first says:"}, first...)
	}
	if want, ok := schema["const"]; ok && fmt.Sprint(want) != fmt.Sprint(value) {
		return []string{fmt.Sprintf("%s: %v, want %v", at, value, want)}
	}
	if kinds, ok := schema["type"]; ok {
		var allowed []string
		switch kinds := kinds.(type) {
		case string:
			allowed = []string{kinds}
		case []any:
			for _, kind := range kinds {
				allowed = append(allowed, kind.(string))
			}
		}
		if !jsonTypeIn(value, allowed) {
			return []string{fmt.Sprintf("%s: %T %v is not %v", at, value, value, allowed)}
		}
	}
	var problems []string
	switch value := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range sortedKeys(value) {
			property, known := properties[name]
			if !known {
				if schema["additionalProperties"] == false {
					problems = append(problems, fmt.Sprintf("%s.%s: not in the schema", at, name))
				}
				continue
			}
			problems = append(problems, validateSchema(property.(map[string]any), value[name], at+"."+name)...)
		}
		required, _ := schema["required"].([]any)
		for _, name := range required {
			if _, ok := value[name.(string)]; !ok {
				problems = append(problems, fmt.Sprintf("%s.%s: required", at, name))
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range value {
				problems = append(problems, validateSchema(items, item, fmt.Sprintf("%s[%d]", at, i))...)
			}
		}
	}
	return problems
}

func jsonTypeIn(value any, allowed []string) bool {
	for _, kind := range allowed {
		switch value := value.(type) {
		case nil:
			if kind == "null" {
				return true
			}
		case string:
			if kind == "string" {
				return true
			}
		case bool:
			if kind == "boolean" {
				return true
			}
		case float64:
			if kind == "number" || kind == "integer" && value == math.Trunc(value) {
				return true
			}
		case []any:
			if kind == "array" {
				return true
			}
		case map[string]any:
			if kind == "object" {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// schemaPaths lists the dotted paths of every property a schema names.
func schemaPaths(schema map[string]any, at string, paths map[string]bool) {
	if branches, ok := schema["anyOf"].([]any); ok {
		for _, branch := range branches {
			schemaPaths(branch.(map[string]any), at, paths)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		schemaPaths(items, at, paths)
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, property := range properties {
		path := name
		if at != "" {
			path = at + "." + name
		}
		paths[path] = true
		schemaPaths(property.(map[string]any), path, paths)
	}
}

// TestMCPOutputSchemasDescribeTheResults calls every tool the ways an agent
// does, successes and refusals, and checks each result against the tool's
// output schema, which allows no field it does not name. It also checks that
// every note of a schema names a field that exists.
func TestMCPOutputSchemasDescribeTheResults(t *testing.T) {
	root := inTempWorkingDirectory(t)
	if err := newProject("demo", os.WriteFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("demo/broken.cicada", []byte("cicada 2\ntempo 120\ntrack bass acid {}\npattern riff { a1 . }\nscene main { bass = rif }\nsong { main }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	startMCPIn(t, root+"/demo", false)
	schemas := map[string]map[string]any{}
	for _, tool := range mcpTools() {
		var schema map[string]any
		if err := json.Unmarshal(tool.OutputSchema, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		if schema["type"] != "object" {
			t.Errorf("%s: the output schema is not an object", tool.Name)
		}
		schemas[tool.Name] = schema
	}
	calls := []struct{ tool, arguments string }{
		{"song", `{}`},
		{"song", `{"score":"main.cicada","include":["source","symbols","structure"]}`},
		{"song", `{"score":"../outside.cicada"}`},
		{"check", `{}`},
		{"check", `{"score":"main.cicada","profile":"browser"}`},
		{"check", `{"score":"broken.cicada"}`},
		{"check", `{"score":"-x.cicada"}`},
		{"intents", `{}`},
		{"intents", `{"kinds":["setparam","addtrack","setautomationpoint"]}`},
		{"intents", `{"kinds":["nosuchkind"]}`},
		{"explain", `{"diagnostic":"CICADA-PARAM"}`},
		{"explain", `{"construct":"track"}`},
		{"explain", `{"construct":"pattern.steps"}`},
		{"explain", `{"construct":"graph.lfo"}`},
		{"explain", `{"construct":"nosuchconstruct"}`},
		{"library", `{}`},
		{"library", `{"path":"std/drums"}`},
		{"library", `{"path":"nosuch/library"}`},
		{"params", `{}`},
	}
	for _, call := range calls {
		document, _ := mcpDocument(t, call.tool, call.arguments)
		for _, problem := range validateSchema(schemas[call.tool], document, call.tool) {
			t.Errorf("%s %s: %s", call.tool, call.arguments, problem)
		}
	}
	for tool, notes := range map[string]map[string]string{"song": mcpSongNotes, "check": mcpCheckNotes, "intents": mcpIntentsNotes, "explain": mcpExplainNotes, "library": mcpLibraryNotes} {
		paths := map[string]bool{}
		schemaPaths(schemas[tool], "", paths)
		for path := range notes {
			if !paths[path] {
				t.Errorf("%s: the note for %s names no field", tool, path)
			}
		}
	}
	if !strings.Contains(string(mustMarshal(schemas["check"])), "bytes from the start of the file") {
		t.Error("the check schema does not give the offset's unit")
	}
}
