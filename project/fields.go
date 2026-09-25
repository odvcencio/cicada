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
	Construct  string  `json:"construct"`
	Name       string  `json:"name"`
	Required   bool    `json:"required"`
	Type       string  `json:"type"`
	Unit       *string `json:"unit"`
	Range      *string `json:"range"`
	Default    *string `json:"default"`
	Meaning    string  `json:"meaning"`
	Order      int     `json:"order"`
	Introduced string  `json:"introduced"`
	Profile    string  `json:"profile"`
	Layer      string  `json:"layer"`
	Variant    string  `json:"variant,omitempty"`
}

type ConstructRecord struct {
	Name       string   `json:"name"`
	ChildRoles []string `json:"child_roles"`
	Variants   []string `json:"variants"`
	Introduced string   `json:"introduced"`
	Profile    string   `json:"profile"`
	Layer      string   `json:"layer"`
}

type FieldCatalog struct {
	Format     string            `json:"format"`
	Constructs []ConstructRecord `json:"constructs"`
	Fields     []Field           `json:"fields"`
}

// Fields returns a stable, exhaustive catalog of the semantic interchange IR.
// Source-language constructs will be added as they acquire typed IR fields.
func Fields() (FieldCatalog, error) {
	catalog := FieldCatalog{Format: "cicada.fields/1", Constructs: []ConstructRecord{}, Fields: []Field{}}
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
		record := ConstructRecord{Name: construct, ChildRoles: []string{}, Variants: []string{}, Introduced: "cicada.project/1", Profile: "M0", Layer: "semantic"}
		variantSeen := map[string]bool{}
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
			required := !strings.Contains(field.Tag.Get("json"), ",omitempty")
			if required && field.Tag.Get("default") != "" {
				return fmt.Errorf("%s.%s is required and cannot declare a default", typ.Name(), field.Name)
			}
			if repeatable(field.Type) {
				record.ChildRoles = append(record.ChildRoles, name)
			}
			variant := field.Tag.Get("variant")
			if variant != "" && !variantSeen[variant] {
				variantSeen[variant] = true
				record.Variants = append(record.Variants, variant)
			}
			catalog.Fields = append(catalog.Fields, Field{
				Construct: construct, Name: name, Required: required,
				Type: fieldType(valueType), Unit: nonemptyTag(field.Tag.Get("unit")), Range: nonemptyTag(field.Tag.Get("range")),
				Default: nonemptyTag(field.Tag.Get("default")), Meaning: meaning, Order: i + 1,
				Introduced: "cicada.project/1", Profile: "M0", Layer: "semantic", Variant: variant,
			})
		}
		catalog.Constructs = append(catalog.Constructs, record)
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

func repeatable(typ reflect.Type) bool {
	return typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map
}

func nonemptyTag(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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
