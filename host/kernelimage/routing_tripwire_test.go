package kernelimage_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
)

// Image version 19 will carry return slots C to H (engine.Config.Routing). The
// TinyGo engine ignores Routing without an error (kernel/engine/engine.go,
// NewFromConfig, and returns_disabled.go), which is safe only while no image
// can carry it. This test fails once the newest image version reaches 19
// while returns_disabled.go still marks the TinyGo engine as ignoring Routing.
func TestNoImageVersionCarriesRoutingYet(t *testing.T) {
	disabled, err := os.ReadFile(filepath.Join("..", "..", "kernel", "engine", "returns_disabled.go"))
	if err != nil {
		t.Fatal(err)
	}
	ignores := strings.Contains(string(disabled), "NewFromConfig then ignores Config.Routing")
	if kernelimage.LatestImageVersion >= 19 && ignores {
		t.Fatalf("image version %d exists while the TinyGo engine ignores Routing: make it build return slots C to H or refuse Routing (kernel/engine/returns_disabled.go), then update this test", kernelimage.LatestImageVersion)
	}
	image, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Routing != nil && ignores {
		t.Fatal("a decoded image carries Routing, which the TinyGo engine ignores")
	}
}

// LatestImageVersion must be the newest image version this package declares,
// so the routing tripwire above cannot be skipped by forgetting to raise it.
func TestLatestImageVersionIsTheNewest(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	newest, name := int64(0), ""
	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if !strings.HasSuffix(strings.ToLower(ident.Name), "imageversion") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.INT {
						continue
					}
					if n, err := strconv.ParseInt(literal.Value, 0, 64); err == nil && n > newest {
						newest, name = n, ident.Name
					}
				}
			}
		}
	}
	if newest == 0 {
		t.Fatal("found no image version constant")
	}
	if int64(kernelimage.LatestImageVersion) != newest {
		t.Fatalf("LatestImageVersion is %d, but %s declares version %d: raise LatestImageVersion", kernelimage.LatestImageVersion, name, newest)
	}
}
