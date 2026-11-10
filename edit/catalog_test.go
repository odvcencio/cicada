package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const intentDocsFile = "intent_docs.go"

// TestIntentDocsAreCurrent regenerates intent_docs.go from the doc comments
// of the intent types and the struct types they nest, and fails when the
// committed file differs. CICADA_UPDATE_INTENT_DOCS=1 writes it instead.
func TestIntentDocsAreCurrent(t *testing.T) {
	docs, err := parseIntentDocs(".", catalogTypeNames())
	if err != nil {
		t.Fatal(err)
	}
	want, err := renderIntentDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CICADA_UPDATE_INTENT_DOCS") == "1" {
		if err := os.WriteFile(intentDocsFile, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(intentDocsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale; run CICADA_UPDATE_INTENT_DOCS=1 go test ./edit -run TestIntentDocsAreCurrent", intentDocsFile)
	}
}

// TestIntentCatalogIsComplete fails when a registered intent has no meaning
// or no example, or when its example does not decode as that kind inside an
// envelope cicada apply reads. cmd/cicada's TestIntentCatalogExamplesApply
// also applies the examples to a score.
func TestIntentCatalogIsComplete(t *testing.T) {
	catalog := Catalog()
	if len(catalog) == 0 {
		t.Fatal("empty catalog")
	}
	for _, info := range catalog {
		typ := reflect.TypeOf(registry[info.Kind]()).Elem()
		if !ast.IsExported(typ.Name()) { // test intents such as probe
			continue
		}
		t.Run(info.Kind, func(t *testing.T) {
			if info.Meaning == "" {
				t.Fatalf("%s needs a doc comment that starts %q, followed by what it does", typ.Name(), typ.Name()+" ")
			}
			if len(info.Example) == 0 {
				t.Fatalf("%s's doc comment needs an \"Example: {...}\" line with a payload cicada apply accepts", typ.Name())
			}
			for _, example := range info.Examples {
				intent, err := decodeIntent(example)
				if err != nil {
					t.Fatalf("example does not decode: %v\n%s", err, example)
				}
				if intent.Kind() != info.Kind {
					t.Fatalf("example decodes as %q", intent.Kind())
				}
				envelope := []byte(`{"version":1,"intents":[` + string(example) + `]}`)
				var env Envelope
				if err := json.Unmarshal(envelope, &env); err != nil {
					t.Fatalf("example envelope does not decode: %v", err)
				}
			}
			for _, field := range info.Fields {
				if field.Type == "" {
					t.Errorf("field %s has no type", field.Name)
				}
			}
		})
	}
}

// selectorNames are field names that usually choose an intent's mode or
// operation. A field with a values list and one of these names must be
// marked cicada:"selects", so a new discriminator cannot go without examples.
var selectorNames = map[string]bool{"mode": true, "op": true, "operation": true, "action": true, "kind": true, "sourcekind": true, "effectkind": true, "timing": true, "starter": true, "message": true, "field": true, "modifier": true, "shape": true}

// TestIntentCatalogCoversEverySelectedValue requires, for every field that
// selects an intent's mode, an example for each of its values, and that such
// fields are marked.
func TestIntentCatalogCoversEverySelectedValue(t *testing.T) {
	for _, info := range Catalog() {
		if !ast.IsExported(reflect.TypeOf(registry[info.Kind]()).Elem().Name()) {
			continue
		}
		var examples []map[string]any
		for _, raw := range info.Examples {
			var example map[string]any
			if err := json.Unmarshal(raw, &example); err != nil {
				t.Fatal(err)
			}
			examples = append(examples, example)
		}
		var walk func(fields []IntentField, path []string)
		walk = func(fields []IntentField, path []string) {
			for _, field := range fields {
				at := append(append([]string(nil), path...), field.Name)
				if len(field.Values) > 0 && selectorNames[field.Name] && !field.Selects {
					t.Errorf("%s.%s chooses among %v; mark it cicada:\"selects\" and give an example per value", info.Kind, strings.Join(at, "."), field.Values)
				}
				if field.Selects {
					for _, value := range field.Values {
						covered := false
						for _, example := range examples {
							got, ok := lookupPath(example, at)
							covered = covered || ok && got == value
						}
						if !covered {
							t.Errorf("%s has no example with %s %q", info.Kind, strings.Join(at, "."), value)
						}
					}
				}
				walk(field.Fields, at)
			}
		}
		walk(info.Fields, nil)
	}
}

func lookupPath(value any, path []string) (any, bool) {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		if value, ok = object[key]; !ok {
			return nil, false
		}
	}
	return value, true
}

// TestIntentCatalogValuesNameHandlerLists requires every values tag to name
// a list a handler registered with valueList, so the catalog shows the list
// the handler checks and nothing else.
func TestIntentCatalogValuesNameHandlerLists(t *testing.T) {
	used := map[string]bool{}
	for name := range catalogTypeNames() {
		typ := catalogType(name)
		for i := 0; i < typ.NumField(); i++ {
			list := typ.Field(i).Tag.Get("values")
			if list == "" {
				continue
			}
			if _, ok := valueLists[list]; !ok {
				t.Errorf("%s.%s: values %q names no handler list", name, typ.Field(i).Name, list)
			}
			used[list] = true
		}
	}
	for list := range valueLists {
		if !used[list] {
			t.Errorf("handler list %q is not shown by any field", list)
		}
	}
}

// TestIntentCatalogRejectsUnlistedFields adds a field the catalog does not
// list to every object of every example (an unknown name, and each field of a
// shared struct the kind excludes) and requires the decoder to refuse it.
func TestIntentCatalogRejectsUnlistedFields(t *testing.T) {
	for _, info := range Catalog() {
		typ := reflect.TypeOf(registry[info.Kind]()).Elem()
		if !ast.IsExported(typ.Name()) {
			continue
		}
		for _, raw := range info.Examples {
			rejectsUnlistedFields(t, info, raw)
		}
	}
}

func rejectsUnlistedFields(t *testing.T, info IntentInfo, raw json.RawMessage) {
	t.Helper()
	{
		var example map[string]any
		if err := json.Unmarshal(raw, &example); err != nil {
			t.Fatal(err)
		}
		var objects []map[string]any
		var collect func(map[string]any)
		collect = func(object map[string]any) {
			objects = append(objects, object)
			for _, value := range object {
				switch value := value.(type) {
				case map[string]any:
					collect(value)
				case []any:
					for _, element := range value {
						if inner, ok := element.(map[string]any); ok {
							collect(inner)
						}
					}
				}
			}
		}
		collect(example)
		for i := range objects {
			objects[i]["unlisted_field"] = 1
			data, _ := json.Marshal(example)
			delete(objects[i], "unlisted_field")
			if _, err := decodeIntent(data); err == nil {
				t.Errorf("%s: the decoder accepts an unlisted field in %s", info.Kind, data)
			}
		}
		for _, path := range kindRules[info.Kind].exclude {
			parts := strings.Split(path, ".")
			parent := example
			for _, part := range parts[:len(parts)-1] {
				parent, _ = parent[part].(map[string]any)
			}
			if parent == nil {
				continue
			}
			parent[parts[len(parts)-1]] = "x"
			data, _ := json.Marshal(example)
			delete(parent, parts[len(parts)-1])
			var field *FieldError
			if _, err := decodeIntent(data); !errors.As(err, &field) || field.Field != path {
				t.Errorf("%s: the decoder accepts the excluded field %s: %v", info.Kind, path, err)
			}
		}
	}
}

// catalogType returns the struct type of a catalog type name.
func catalogType(name string) reflect.Type {
	var found reflect.Type
	var visit func(reflect.Type)
	visit = func(typ reflect.Type) {
		typ = indirect(typ)
		if typ.Kind() == reflect.Slice {
			visit(typ.Elem())
			return
		}
		if typ.Kind() != reflect.Struct || found != nil {
			return
		}
		if typ.Name() == name {
			found = typ
			return
		}
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				visit(typ.Field(i).Type)
			}
		}
	}
	for _, kind := range Kinds() {
		visit(reflect.TypeOf(registry[kind]()))
	}
	return found
}

// catalogTypeNames lists the Go names of the intent types and of the struct
// types of this package that their fields nest.
func catalogTypeNames() map[string]bool {
	names := map[string]bool{}
	pkg := reflect.TypeOf(IntentInfo{}).PkgPath()
	var visit func(reflect.Type)
	visit = func(typ reflect.Type) {
		typ = indirect(typ)
		if typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			visit(typ.Elem())
			return
		}
		if typ.Kind() != reflect.Struct || typ.PkgPath() != pkg || names[typ.Name()] {
			return
		}
		names[typ.Name()] = true
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).IsExported() {
				visit(typ.Field(i).Type)
			}
		}
	}
	for _, kind := range Kinds() {
		if typ := reflect.TypeOf(registry[kind]()); ast.IsExported(indirect(typ).Name()) { // not a test intent
			visit(typ)
		}
	}
	return names
}

// parseIntentDocs reads the doc comments of the named struct types from the
// package's non-test Go files.
func parseIntentDocs(dir string, names map[string]bool) (map[string]typeDoc, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	docs := map[string]typeDoc{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == intentDocsFile {
			continue
		}
		file, err := parser.ParseFile(fset, dir+"/"+name, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec := spec.(*ast.TypeSpec)
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok || !names[typeSpec.Name.Name] {
					continue
				}
				comment := typeSpec.Doc
				if comment == nil && len(general.Specs) == 1 {
					comment = general.Doc
				}
				doc, err := typeDocFromComment(typeSpec.Name.Name, comment.Text())
				if err != nil {
					return nil, fmt.Errorf("%s: %w", fset.Position(typeSpec.Pos()), err)
				}
				for _, field := range structType.Fields.List {
					text := strings.TrimSpace(field.Doc.Text() + " " + field.Comment.Text())
					if text == "" {
						continue
					}
					for _, ident := range field.Names {
						if ident.IsExported() {
							if doc.Fields == nil {
								doc.Fields = map[string]string{}
							}
							doc.Fields[ident.Name] = oneLine(text)
						}
					}
				}
				docs[typeSpec.Name.Name] = doc
			}
		}
	}
	return docs, nil
}

// typeDocFromComment splits a doc comment into the meaning (its first
// sentence without the type name), the notes (the other sentences) and the
// example (the line that starts "Example: ").
func typeDocFromComment(name, text string) (typeDoc, error) {
	var doc typeDoc
	var prose []string
	for _, line := range strings.Split(text, "\n") {
		if example, ok := strings.CutPrefix(strings.TrimSpace(line), "Example: "); ok {
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(example)); err != nil {
				return typeDoc{}, fmt.Errorf("%s example is not JSON: %w", name, err)
			}
			doc.Examples = append(doc.Examples, compact.String())
			continue
		}
		prose = append(prose, line)
	}
	body := oneLine(strings.Join(prose, " "))
	rest, ok := strings.CutPrefix(body, name+" ")
	if !ok {
		return doc, nil // the completeness test names the missing meaning
	}
	sentence, notes := rest, ""
	for i := 0; i < len(rest); i++ {
		if rest[i] == '.' && (i+1 == len(rest) || rest[i+1] == ' ') {
			sentence, notes = rest[:i], strings.TrimSpace(rest[i+1:])
			break
		}
	}
	doc.Meaning, doc.Notes = sentence, notes
	return doc, nil
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func renderIntentDocs(docs map[string]typeDoc) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("// Code generated by TestIntentDocsAreCurrent from the doc comments of the\n")
	out.WriteString("// intent types; DO NOT EDIT. After changing a doc comment, run\n")
	out.WriteString("//\n//\tCICADA_UPDATE_INTENT_DOCS=1 go test ./edit -run TestIntentDocsAreCurrent\n\n")
	out.WriteString("package edit\n\nvar intentDocs = map[string]typeDoc{\n")
	names := make([]string, 0, len(docs))
	for name := range docs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		doc := docs[name]
		fmt.Fprintf(&out, "%s: {\n", strconv.Quote(name))
		if doc.Meaning != "" {
			fmt.Fprintf(&out, "Meaning: %s,\n", strconv.Quote(doc.Meaning))
		}
		if doc.Notes != "" {
			fmt.Fprintf(&out, "Notes: %s,\n", strconv.Quote(doc.Notes))
		}
		if len(doc.Examples) > 0 {
			out.WriteString("Examples: []string{\n")
			for _, example := range doc.Examples {
				fmt.Fprintf(&out, "%s,\n", strconv.Quote(example))
			}
			out.WriteString("},\n")
		}
		if len(doc.Fields) > 0 {
			fields := make([]string, 0, len(doc.Fields))
			for field := range doc.Fields {
				fields = append(fields, field)
			}
			sort.Strings(fields)
			out.WriteString("Fields: map[string]string{\n")
			for _, field := range fields {
				fmt.Fprintf(&out, "%s: %s,\n", strconv.Quote(field), strconv.Quote(doc.Fields[field]))
			}
			out.WriteString("},\n")
		}
		out.WriteString("},\n")
	}
	out.WriteString("}\n")
	return format.Source(out.Bytes())
}
