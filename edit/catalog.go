package edit

import (
	"encoding/json"
	"reflect"
	"strings"
)

// The intent catalog describes every registered intent kind for people and
// agents: what it does, its JSON fields and one payload cicada apply accepts.
// It is built from the registry, the intent structs (by reflection over their
// JSON tags) and the structs' doc comments, which intent_docs.go carries into
// the binary. A doc comment has this form:
//
//	// SetStep writes one step. Further sentences become the notes.
//	//
//	// Example: {"kind":"setstep","entity":"step:riff/1","mode":"note","pitch":45}
//
// The first sentence, without the type name, is the meaning. A field's doc
// or line comment is its meaning. Struct tags add what the handler checks:
//
//   - cicada:"required": the intent is refused without the field;
//   - when:"<field>=<a>|<b>": the field is required when another field of the
//     same intent has one of those values;
//   - values:"<list>": the accepted strings, from a list the handler itself
//     checks (see valueLists);
//   - unit:"...": the unit of a number.
//
// A struct that several kinds share (PlacementSettings) gets per-kind rules
// from kindRules instead. TestIntentDocsAreCurrent regenerates intent_docs.go;
// cmd/cicada's TestIntentCatalogFieldsAreTrue proves the required, optional
// and values claims against cicada apply for every kind.

// IntentInfo describes one intent kind. OneOf lists groups of field paths of
// which the intent needs at least one each.
type IntentInfo struct {
	Kind    string          `json:"kind"`
	Meaning string          `json:"meaning"`
	Notes   string          `json:"notes,omitempty"`
	Fields  []IntentField   `json:"fields"`
	OneOf   [][]string      `json:"one_of,omitempty"`
	Example json.RawMessage `json:"example"`
}

// IntentField describes one JSON field of an intent or of a nested object.
// Fields keep the order of the Go struct.
//
// Required is true when the intent is refused without the field. A field
// that is not required may be left out: the decoder then reads its zero
// value (0, "", false, null or an empty list), which the intent treats as the
// default its meaning or notes state. RequiredWhen names the condition under
// which an otherwise optional field becomes required.
type IntentField struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Items        string          `json:"items,omitempty"`
	Required     bool            `json:"required"`
	RequiredWhen *FieldCondition `json:"required_when,omitempty"`
	Unit         string          `json:"unit,omitempty"`
	Values       []string        `json:"values,omitempty"`
	Meaning      string          `json:"meaning,omitempty"`
	Fields       []IntentField   `json:"fields,omitempty"`
}

// FieldCondition holds when another field of the same intent (Field, a
// dotted path from the intent's top level) has one of Values.
type FieldCondition struct {
	Field  string   `json:"field"`
	Values []string `json:"values"`
}

// fieldRules are what a kind requires of a struct it shares with other kinds.
// Paths are dotted JSON names from the intent's top level.
type fieldRules struct {
	required []string   // always required
	exclude  []string   // fields of the shared struct this kind refuses
	oneOf    [][]string // groups of which at least one field is required
}

// kindRules holds the rules of the kinds that need them. The decoder refuses
// an excluded field as it refuses an unknown one.
var kindRules = map[string]fieldRules{}

func ruleKind(kind string, rules fieldRules) { kindRules[kind] = rules }

// valueLists are the accepted strings of the fields whose values tag names
// them. Each list is the one its handler checks against, so the catalog and
// the handler cannot disagree.
var valueLists = map[string][]string{}

// valueList registers a handler's list under name and returns it.
func valueList(name string, values ...string) []string {
	valueLists[name] = values
	return values
}

// typeDoc is what the doc comments of one struct type say. intent_docs.go
// holds one per intent type and per struct type an intent nests.
type typeDoc struct {
	Meaning string
	Notes   string
	Example string
	Fields  map[string]string
}

// Catalog lists every registered intent kind in sorted order.
func Catalog() []IntentInfo {
	kinds := Kinds()
	catalog := make([]IntentInfo, 0, len(kinds))
	for _, kind := range kinds {
		catalog = append(catalog, describeIntent(kind))
	}
	return catalog
}

// DescribeIntent returns the catalog entry of one kind.
func DescribeIntent(kind string) (IntentInfo, bool) {
	if _, ok := registry[kind]; !ok {
		return IntentInfo{}, false
	}
	return describeIntent(kind), true
}

func describeIntent(kind string) IntentInfo {
	typ := reflect.TypeOf(registry[kind]())
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	doc := intentDocs[typ.Name()]
	rules := kindRules[kind]
	info := IntentInfo{Kind: kind, Meaning: doc.Meaning, Notes: doc.Notes, Fields: describeFields(typ, "", rules, 0), OneOf: rules.oneOf}
	if doc.Example != "" {
		info.Example = json.RawMessage(doc.Example)
	}
	return info
}

// EnvelopeFields describes the JSON envelope that carries intents to apply.
func EnvelopeFields() []IntentField {
	return []IntentField{
		{Name: "version", Type: "integer", Required: true, Values: []string{"1"}, Meaning: "envelope format version"},
		{Name: "revision", Type: "string", Meaning: "revision the intents were computed against (the SHA-256 of the score bytes, in hex); apply refuses the batch when the score has changed since"},
		{Name: "author", Type: "string", Meaning: "who made the edit, recorded in the edit log (default: cli)"},
		{Name: "session", Type: "string", Meaning: "session identifier recorded in the edit log"},
		{Name: "dryrun", Type: "boolean", Meaning: "validate and print the diff but never write, even with --write"},
		{Name: "intents", Type: "array", Items: "object", Required: true, Meaning: "the intents, applied in order as one revision; each object carries its kind and the fields of that kind"},
	}
}

const maxCatalogDepth = 4

func describeFields(typ reflect.Type, prefix string, rules fieldRules, depth int) []IntentField {
	fields := []IntentField{}
	if typ.Kind() != reflect.Struct || depth > maxCatalogDepth {
		return fields
	}
	doc := intentDocs[typ.Name()]
	if typ.PkgPath() != reflect.TypeOf(IntentInfo{}).PkgPath() {
		doc = typeDoc{}
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			fields = append(fields, describeFields(indirect(field.Type), prefix, rules, depth)...)
			continue
		}
		if name == "" {
			name = field.Name
		}
		path := prefix + name
		if containsString(rules.exclude, path) {
			continue
		}
		described := IntentField{
			Name:     name,
			Required: field.Tag.Get("cicada") == "required" || containsString(rules.required, path),
			Unit:     field.Tag.Get("unit"),
			Meaning:  doc.Fields[field.Name],
		}
		if when := field.Tag.Get("when"); when != "" {
			other, values, _ := strings.Cut(when, "=")
			described.RequiredWhen = &FieldCondition{Field: other, Values: strings.Split(values, "|")}
		}
		if list := field.Tag.Get("values"); list != "" {
			described.Values = valueLists[list]
		}
		described.Type, described.Items = jsonType(field.Type)
		element := indirect(field.Type)
		if element.Kind() == reflect.Slice {
			element = indirect(element.Elem())
		}
		if element.Kind() == reflect.Struct && described.Type != "entity" {
			described.Fields = describeFields(element, path+".", rules, depth+1)
		}
		fields = append(fields, described)
	}
	return fields
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func indirect(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

var (
	entityIDType   = reflect.TypeOf(EntityID(""))
	rawMessageType = reflect.TypeOf(json.RawMessage(nil))
)

// jsonType names a Go type as the JSON it decodes from, and the element type
// of a list.
func jsonType(typ reflect.Type) (string, string) {
	typ = indirect(typ)
	switch {
	case typ == entityIDType:
		return "entity", ""
	case typ == rawMessageType:
		return "any", ""
	}
	switch typ.Kind() {
	case reflect.String:
		return "string", ""
	case reflect.Bool:
		return "boolean", ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer", ""
	case reflect.Float32, reflect.Float64:
		return "number", ""
	case reflect.Slice, reflect.Array:
		items, _ := jsonType(typ.Elem())
		return "array", items
	case reflect.Map, reflect.Struct:
		return "object", ""
	}
	return "any", ""
}
