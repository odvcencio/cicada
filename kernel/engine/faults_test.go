package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// retiredFaultCodes once carried two to five unrelated meanings each. Old log
// lines that hold them cannot be decoded, so no fault may use them again.
var retiredFaultCodes = []uint16{12, 13, 14, 15, 16, 17, 18, 19}

type faultConstant struct {
	name  string
	value uint16
	where string
}

// faultConstants reads the fault codes from source instead of from a table in
// the test, so a new constant cannot be left out of the checks. Codes must be
// explicit uint16 literals: hosts and logs match on the number.
func faultConstants(t *testing.T) []faultConstant {
	t.Helper()
	var found []faultConstant
	for _, source := range []struct {
		path string
		// all is true when every constant in the file is a fault code. In
		// kernel/cmd only uint16 constants named Fault<Name> are fault codes;
		// the message kind cmd.Fault is not.
		all bool
	}{{"faults.go", true}, {filepath.Join("..", "cmd", "command.go"), false}} {
		file, err := parser.ParseFile(token.NewFileSet(), source.path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			block, ok := decl.(*ast.GenDecl)
			if !ok || block.Tok != token.CONST {
				continue
			}
			for _, spec := range block.Specs {
				value := spec.(*ast.ValueSpec)
				typ, _ := value.Type.(*ast.Ident)
				isUint16 := typ != nil && typ.Name == "uint16"
				for i, name := range value.Names {
					fault := strings.HasPrefix(name.Name, "Fault") && len(name.Name) > len("Fault") && isUint16
					if !source.all && !fault {
						continue
					}
					where := source.path + ":" + name.Name
					if !isUint16 || i >= len(value.Values) {
						t.Fatalf("%s must be declared as a uint16 with an explicit number", where)
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.INT {
						t.Fatalf("%s must be set to a number literal, not an expression", where)
					}
					number, err := strconv.ParseUint(literal.Value, 0, 16)
					if err != nil {
						t.Fatalf("%s: %v", where, err)
					}
					found = append(found, faultConstant{name: name.Name, value: uint16(number), where: source.path})
				}
			}
		}
	}
	return found
}

func TestFaultCodesAreUnique(t *testing.T) {
	codes := faultConstants(t)
	if len(codes) < 30 {
		t.Fatalf("found only %d fault codes; the parser is missing declarations", len(codes))
	}
	owners := map[uint16][]string{}
	for _, code := range codes {
		if code.value == 0 {
			t.Errorf("%s uses 0, which means no fault", code.name)
		}
		owners[code.value] = append(owners[code.value], code.name)
	}
	var numbers []int
	for number := range owners {
		numbers = append(numbers, int(number))
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		if names := owners[uint16(number)]; len(names) > 1 {
			t.Errorf("fault code %d is shared by %s", number, strings.Join(names, ", "))
		}
	}
}

func TestRetiredFaultCodesStayUnused(t *testing.T) {
	retired := map[uint16]bool{}
	for _, number := range retiredFaultCodes {
		retired[number] = true
	}
	for _, code := range faultConstants(t) {
		if retired[code.value] {
			t.Errorf("%s reuses retired fault code %d", code.name, code.value)
		}
	}
}

// Number literals are how fault 14 came to mean both a delay fault and a bad
// quantize value. Engine code must pass a named constant, so the list in
// faults.go stays complete.
func TestFaultsUseNamedConstants(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				selector, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "fault" && selector.Sel.Name != "InjectFault" {
					break
				}
				checked++
				for _, arg := range node.Args {
					if _, literal := arg.(*ast.BasicLit); literal {
						t.Errorf("%s: %s takes a number literal; use a Fault constant from faults.go", fset.Position(arg.Pos()), selector.Sel.Name)
					}
				}
			case *ast.CompositeLit:
				// A cmd.Message built by hand with Kind cmd.Fault carries a code in A.
				keys := map[string]ast.Expr{}
				for _, element := range node.Elts {
					if pair, ok := element.(*ast.KeyValueExpr); ok {
						if key, ok := pair.Key.(*ast.Ident); ok {
							keys[key.Name] = pair.Value
						}
					}
				}
				kind, ok := keys["Kind"].(*ast.SelectorExpr)
				if !ok || kind.Sel.Name != "Fault" {
					break
				}
				checked++
				if _, literal := keys["A"].(*ast.BasicLit); literal {
					t.Errorf("%s: a fault message takes a number literal in A; use a Fault constant from faults.go", fset.Position(node.Pos()))
				}
			}
			return true
		})
	}
	if checked < 50 {
		t.Fatalf("found only %d fault sites; the scan is missing code", checked)
	}
}
