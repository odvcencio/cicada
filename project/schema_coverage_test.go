package project

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The field registry will be generated from the typed IR. Until that
// generator owns project-1.json, keep every serialized field visible in the
// current interchange schema, including fields of nested and union types.
func TestSemanticIRFieldsMatchProjectSchema(t *testing.T) {
	data, err := os.ReadFile("schema/project-1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	defs, ok := schema["$defs"].(map[string]any)
	if !ok {
		t.Fatal("project schema has no $defs")
	}
	visited := map[reflect.Type]bool{}
	var visit func(reflect.Type)
	visit = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || visited[typ] {
			return
		}
		visited[typ] = true
		construct := snakeCase(typ.Name())
		definition := any(schema)
		if typ != reflect.TypeFor[Project]() {
			definition = defs[construct]
			if definition == nil {
				t.Errorf("%s has no schema definition", typ.Name())
				return
			}
		}
		want := map[string]bool{}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				t.Errorf("%s.%s has no serialized field name", typ.Name(), field.Name)
				continue
			}
			want[name] = true
			visit(field.Type)
		}
		got := schemaProperties(definition, defs)
		if !reflect.DeepEqual(sortedFields(want), sortedFields(got)) {
			t.Errorf("%s fields: IR %v, schema %v", typ.Name(), sortedFields(want), sortedFields(got))
		}
		if object, ok := definition.(map[string]any); ok && object["properties"] != nil {
			required := map[string]bool{}
			if list, ok := object["required"].([]any); ok {
				for _, name := range list {
					if name, ok := name.(string); ok {
						required[name] = true
					}
				}
			}
			if !reflect.DeepEqual(sortedFields(want), sortedFields(required)) {
				t.Errorf("%s required fields: IR %v, schema %v", typ.Name(), sortedFields(want), sortedFields(required))
			}
		}
	}
	visit(reflect.TypeFor[Project]())
}

func schemaProperties(value any, defs map[string]any) map[string]bool {
	fields := map[string]bool{}
	object, ok := value.(map[string]any)
	if !ok {
		return fields
	}
	if reference, ok := object["$ref"].(string); ok {
		return schemaProperties(defs[strings.TrimPrefix(reference, "#/$defs/")], defs)
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		for name := range properties {
			fields[name] = true
		}
	}
	if variants, ok := object["oneOf"].([]any); ok {
		for _, variant := range variants {
			for name := range schemaProperties(variant, defs) {
				fields[name] = true
			}
		}
	}
	return fields
}

func sortedFields(fields map[string]bool) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func snakeCase(name string) string {
	var output strings.Builder
	for index, character := range name {
		if character >= 'A' && character <= 'Z' {
			if index > 0 {
				output.WriteByte('_')
			}
			output.WriteByte(byte(character + ('a' - 'A')))
		} else {
			output.WriteRune(character)
		}
	}
	return output.String()
}
