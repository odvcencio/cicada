package edit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// reasonConstructors build a refusal from a reason code given as their first
// argument: refuse (a ReasonError; notes.go also has a local refuse for note
// refusals) and the NoteError constructors of editnotes.
var reasonConstructors = map[string]bool{"refuse": true, "performedNoteError": true, "noteAt": true, "hitAt": true, "refAt": true}

// handlerReasons derives, from the package source, the reason codes each
// handler can return: the string literals given to a reason constructor or
// set as a ReasonError's Reason or a NoteError's Code, in the handler and in
// every package function and method it may call. A method call is followed
// when the receiver's type is known from a parameter, a type assertion or a
// composite literal. A reason that is not a literal is reported, since no
// list could name it.
func handlerReasons(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	funcs := map[string]*ast.FuncDecl{} // "name" or "Type.name"
	var handles []*ast.CallExpr
	typeName := func(expr ast.Expr) string {
		for {
			switch e := expr.(type) {
			case *ast.StarExpr:
				expr = e.X
			case *ast.UnaryExpr:
				expr = e.X
			case *ast.CompositeLit:
				expr = e.Type
			case *ast.Ident:
				return e.Name
			default:
				return ""
			}
		}
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				key = typeName(fn.Recv.List[0].Type) + "." + key
			}
			funcs[key] = fn
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "Handle" && len(call.Args) == 2 {
						handles = append(handles, call)
					}
				}
				return true
			})
		}
	}
	literal := func(expr ast.Expr) (string, bool) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(lit.Value)
		return value, err == nil
	}
	reasonsOf := func(root ast.Node, params *ast.FieldList) []string {
		seen := map[string]bool{}
		found := map[string]bool{}
		var walk func(node ast.Node, params *ast.FieldList, receiver *ast.FieldList)
		walk = func(node ast.Node, params *ast.FieldList, receiver *ast.FieldList) {
			types := map[string]string{} // variable -> type
			for _, list := range []*ast.FieldList{params, receiver} {
				if list == nil {
					continue
				}
				for _, field := range list.List {
					for _, name := range field.Names {
						types[name.Name] = typeName(field.Type)
					}
				}
			}
			ast.Inspect(node, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					for i, rhs := range n.Rhs {
						if i >= len(n.Lhs) {
							break
						}
						ident, ok := n.Lhs[i].(*ast.Ident)
						if !ok {
							continue
						}
						switch value := rhs.(type) {
						case *ast.TypeAssertExpr:
							types[ident.Name] = typeName(value.Type)
						case *ast.UnaryExpr, *ast.CompositeLit:
							types[ident.Name] = typeName(value)
						}
					}
				case *ast.CallExpr:
					name, key := "", ""
					switch fun := n.Fun.(type) {
					case *ast.Ident:
						name, key = fun.Name, fun.Name
					case *ast.SelectorExpr:
						name = fun.Sel.Name
						if x, ok := fun.X.(*ast.Ident); ok && types[x.Name] != "" {
							key = types[x.Name] + "." + name
						}
					}
					if reasonConstructors[name] && len(n.Args) > 0 {
						if reason, ok := literal(n.Args[0]); ok {
							found[reason] = true
						} else if _, ok := n.Args[0].(*ast.Ident); !ok || !strings.Contains(fset.Position(n.Pos()).Filename, "notes.go") {
							t.Errorf("%s: %s is given a reason that is not a literal", fset.Position(n.Pos()), name)
						}
					}
					// A constructor builds its error from its argument, checked above.
					if fn := funcs[key]; key != "" && fn != nil && !reasonConstructors[name] && !seen[key] {
						seen[key] = true
						walk(fn.Body, fn.Type.Params, fn.Recv)
					}
				case *ast.CompositeLit:
					if ident, ok := n.Type.(*ast.Ident); ok && (ident.Name == "ReasonError" || ident.Name == "NoteError") {
						for _, element := range n.Elts {
							if kv, ok := element.(*ast.KeyValueExpr); ok && (kv.Key.(*ast.Ident).Name == "Reason" || kv.Key.(*ast.Ident).Name == "Code") {
								if reason, ok := literal(kv.Value); ok {
									found[reason] = true
								} else {
									t.Errorf("%s: a %s's reason is not a literal", fset.Position(kv.Pos()), ident.Name)
								}
							}
						}
					}
				}
				return true
			})
		}
		walk(root, params, nil)
		reasons := make([]string, 0, len(found))
		for reason := range found {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		return reasons
	}
	byKind := map[string][]string{}
	for _, call := range handles {
		kind, ok := literal(call.Args[0])
		if !ok {
			continue
		}
		var reasons []string
		switch handler := call.Args[1].(type) {
		case *ast.Ident:
			if fn := funcs[handler.Name]; fn != nil {
				reasons = reasonsOf(fn.Body, fn.Type.Params)
			}
		case *ast.FuncLit:
			reasons = reasonsOf(handler.Body, handler.Type.Params)
		}
		byKind[kind] = slices.Compact(reasons)
	}
	return byKind
}

// TestIntentReasonsAreDeclared keeps each kind's catalog reasons equal to the
// reason codes its handler can return, so an agent can match on the list.
//
// CICADA_UPDATE_INTENT_REASONS=1 rewrites reasons.go from the handlers.
func TestIntentReasonsAreDeclared(t *testing.T) {
	derived := handlerReasons(t)
	if os.Getenv("CICADA_UPDATE_INTENT_REASONS") == "1" {
		var out strings.Builder
		out.WriteString("// Code generated by CICADA_UPDATE_INTENT_REASONS=1 go test ./edit -run TestIntentReasonsAreDeclared. DO NOT EDIT.\n\npackage edit\n\n")
		out.WriteString("// The reason codes each kind's refusals can carry, in the catalog's reasons\n// list, derived from the handlers' code.\nfunc init() {\n")
		kinds := make([]string, 0, len(derived))
		for kind, reasons := range derived {
			if len(reasons) > 0 {
				kinds = append(kinds, kind)
			}
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			quoted := make([]string, len(derived[kind]))
			for i, reason := range derived[kind] {
				quoted[i] = strconv.Quote(reason)
			}
			out.WriteString("\tdeclareReasons(" + strconv.Quote(kind) + ", " + strings.Join(quoted, ", ") + ")\n")
		}
		out.WriteString("}\n")
		if err := os.WriteFile("reasons.go", []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, kind := range Kinds() {
		want := derived[kind]
		got := append([]string(nil), intentReasons[kind]...)
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: the catalog lists reasons %v; the handler can return %v", kind, got, want)
		}
	}
}
