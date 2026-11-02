package modulate

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// New audio-thread code makes no math-library call (spec 16, spec 24 "Rules
// shared by all four lanes"), so the package may import neither math nor a
// float type's helpers; math/bits is integer-only and allowed.
func TestPackageImportsNoMath(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, err := os.Stat(name); err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"math"` || imp.Path.Value == `"math/big"` {
				t.Errorf("%s imports %s", name, imp.Path.Value)
			}
		}
	}
}
