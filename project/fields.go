package project

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Field describes one serialized field of the typed semantic project model.
// The JSON and cicada tags on model.go are its source of truth.
type Field struct {
	Construct  string `json:"construct"`
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Type       string `json:"type"`
	Unit       string `json:"unit"`
	Range      string `json:"range"`
	Default    string `json:"default"`
	Meaning    string `json:"meaning"`
	Order      int    `json:"order"`
	Introduced string `json:"introduced"`
	Profile    string `json:"profile"`
	Variant    string `json:"variant,omitempty"`
}

type FieldCatalog struct {
	Format string  `json:"format"`
	Fields []Field `json:"fields"`
}

// Fields returns a stable, exhaustive catalog of the semantic interchange IR.
// Source-language constructs will be added as they acquire typed IR fields.
func Fields() (FieldCatalog, error) {
	catalog := FieldCatalog{Format: "cicada.fields/1", Fields: []Field{}}
	seen := map[reflect.Type]bool{}
	var visit func(reflect.Type) error
	visit = func(typ reflect.Type) error {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return nil
		}
		seen[typ] = true
		construct := snakeCaseName(typ.Name())
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			meaning := field.Tag.Get("cicada")
			if name == "" || name == "-" || meaning == "" {
				return fmt.Errorf("%s.%s needs JSON name and Cicada meaning", typ.Name(), field.Name)
			}
			valueType := field.Type
			if field.Tag.Get("variant") != "" && valueType.Kind() == reflect.Pointer {
				valueType = valueType.Elem()
			}
			catalog.Fields = append(catalog.Fields, Field{
				Construct: construct, Name: name, Required: true,
				Type: fieldType(valueType), Unit: field.Tag.Get("unit"), Range: field.Tag.Get("range"),
				Default: field.Tag.Get("default"), Meaning: meaning, Order: i + 1,
				Introduced: "cicada.project/1", Profile: "semantic", Variant: field.Tag.Get("variant"),
			})
		}
		for i := 0; i < typ.NumField(); i++ {
			if err := visit(typ.Field(i).Type); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(reflect.TypeFor[Project]()); err != nil {
		return FieldCatalog{}, err
	}
	return catalog, nil
}

func FieldsJSON() ([]byte, error) {
	catalog, err := Fields()
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(catalog, "", "  ")
}

func fieldType(typ reflect.Type) string {
	switch typ.Kind() {
	case reflect.Pointer:
		return "nullable<" + fieldType(typ.Elem()) + ">"
	case reflect.Slice, reflect.Array:
		return "array<" + fieldType(typ.Elem()) + ">"
	case reflect.Map:
		return "map<" + fieldType(typ.Key()) + "," + fieldType(typ.Elem()) + ">"
	case reflect.Struct:
		return snakeCaseName(typ.Name())
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	default:
		return typ.String()
	}
}

func snakeCaseName(name string) string {
	var out strings.Builder
	for i, c := range name {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteByte(byte(c + ('a' - 'A')))
		} else {
			out.WriteRune(c)
		}
	}
	return out.String()
}
