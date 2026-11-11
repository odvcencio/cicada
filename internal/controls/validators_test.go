package controls_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// validatorLiterals are the string literals of the notation and project
// validators: every name a validator could compare a setting with.
func validatorLiterals(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{"../../notation", "../../project"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if text, err := strconv.Unquote(lit.Value); err == nil && len(text) <= 32 {
						out = append(out, text)
					}
				}
				return true
			})
		}
	}
	return out
}

// effectCompilers maps each effect compiler in package project to the effect
// kind it reads. masterEffectParams reads several kinds; its labels are
// written kind.name.
var effectCompilers = map[string]string{
	"DriveParamsFromValues": "drive", "DelayParamsFromValues": "delay", "ReverbParamsFromValues": "reverb",
	"CompSpecFromValues": "comp", "ChorusParamsFromValues": "chorus", "DeesserParamsFromValues": "deesser",
	"AmpCabParamsFromValues": "ampcab", "TapeParamsFromValues": "tape", "convolutionSpec": "convolution",
	"masterEffectParams": "",
}

// effectCompilerLabels reads the setting names each effect compiler accepts:
// the case labels of its switch on name (or on kind + "." + name) and the
// names it compares name with directly.
func effectCompilerLabels(dir string) (map[string]map[string]bool, error) {
	labels := map[string]map[string]bool{}
	add := func(kind, name string) {
		if kind == "" {
			var ok bool
			kind, name, ok = strings.Cut(name, ".")
			if !ok {
				return
			}
		}
		if labels[kind] == nil {
			labels[kind] = map[string]bool{}
		}
		labels[kind][name] = true
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			kind, ok := effectCompilers[fn.Name.Name]
			if !ok {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SwitchStmt:
					if !switchesOnName(n.Tag) {
						return true
					}
					for _, stmt := range n.Body.List {
						for _, expr := range stmt.(*ast.CaseClause).List {
							if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								text, _ := strconv.Unquote(lit.Value)
								add(kind, text)
							}
						}
					}
				case *ast.BinaryExpr:
					if n.Op == token.EQL {
						if ident, ok := n.X.(*ast.Ident); ok && ident.Name == "name" {
							if lit, ok := n.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								text, _ := strconv.Unquote(lit.Value)
								add(kind, text)
							}
						}
					}
				}
				return true
			})
		}
	}
	return labels, nil
}

// switchesOnName reports a switch on name or on an expression that ends in name.
func switchesOnName(tag ast.Expr) bool {
	switch tag := tag.(type) {
	case *ast.Ident:
		return tag.Name == "name"
	case *ast.BinaryExpr:
		return switchesOnName(tag.Y)
	}
	return false
}
