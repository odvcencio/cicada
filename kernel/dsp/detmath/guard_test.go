package detmath

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The render path must not call a math library function whose last bits can
// differ by target: Go's math.Exp, Log, Pow, Sin, Cos, Tan, Tanh and the rest
// come from different code on amd64 (where math.Exp even takes an FMA path only
// on CPUs that have one), arm64 and WebAssembly. This test parses every
// non-test Go file under kernel/ and render/, plus the loudness search in
// cmd/cicada/main.go and the automation curves in project/automation.go, and
// fails on any use of a math function that is not in exactFunctions. Those
// return the same bits on every target: IEEE 754 operations that are correctly
// rounded (Sqrt, FMA), conversions to and from bits, and exact operations
// (Abs, Floor, Ldexp, Mod and the like). This package replaces the others.
//
// mathAllowlist names files that may still use other math functions, with the
// exact number of uses: a new use fails, and a removed one asks for the entry
// to be lowered. It is empty since 2026-11-10 (Decision 9 of
// hypha://m31labs/cicada, decisions/2026-11-10-stewardship-and-direction) and
// should stay so.
//
// notRenderPath names files that never produce audio: analysis and display
// code, whose results are read by people and agents but never rendered.

var exactFunctions = map[string]bool{
	"Abs": true, "Ceil": true, "Copysign": true, "Dim": true, "FMA": true,
	"Float32bits": true, "Float32frombits": true, "Float64bits": true, "Float64frombits": true,
	"Floor": true, "Frexp": true, "Ilogb": true, "Inf": true, "IsInf": true, "IsNaN": true,
	"Ldexp": true, "Logb": true, "Max": true, "Min": true, "Mod": true, "Modf": true,
	"NaN": true, "Nextafter": true, "Nextafter32": true, "Remainder": true,
	"Round": true, "RoundToEven": true, "Signbit": true, "Sqrt": true, "Trunc": true,
}

// mathConstants are the exported constants of package math; they are values,
// not functions, and are the same on every target.
var mathConstants = map[string]bool{
	"E": true, "Pi": true, "Phi": true, "Sqrt2": true, "SqrtE": true, "SqrtPi": true, "SqrtPhi": true,
	"Ln2": true, "Log2E": true, "Ln10": true, "Log10E": true,
	"MaxFloat32": true, "SmallestNonzeroFloat32": true, "MaxFloat64": true, "SmallestNonzeroFloat64": true,
	"MaxInt": true, "MinInt": true, "MaxInt8": true, "MinInt8": true, "MaxInt16": true, "MinInt16": true,
	"MaxInt32": true, "MinInt32": true, "MaxInt64": true, "MinInt64": true,
	"MaxUint": true, "MaxUint8": true, "MaxUint16": true, "MaxUint32": true, "MaxUint64": true,
}

var mathAllowlist = map[string]int{}

var notRenderPath = map[string]string{
	"kernel/analysis/correlation.go": "analysis for Studio meters and audition reports",
	"kernel/analysis/fft.go":         "analysis for Studio meters and audition reports",
	"kernel/analysis/spectrum.go":    "analysis for Studio meters and audition reports",
	"render/fingerprint.go":          "an audio fingerprint for comparing renders; it reads audio, it does not make it",
}

var guardedFiles = []string{"cmd/cicada/main.go", "project/automation.go"}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found from %s: %v", root, err)
	}
	return root
}

// inexactUses returns the uses of math functions outside exactFunctions in a
// Go file, as "Name" entries in source order.
func inexactUses(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for _, spec := range file.Imports {
		if p, _ := strconv.Unquote(spec.Path.Value); p == "math" {
			name = "math"
			if spec.Name != nil {
				name = spec.Name.Name
			}
		}
	}
	if name == "" || name == "_" {
		return nil
	}
	var uses []string
	ast.Inspect(file, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := selector.X.(*ast.Ident); ok && x.Name == name {
			if !exactFunctions[selector.Sel.Name] && !mathConstants[selector.Sel.Name] {
				uses = append(uses, selector.Sel.Name)
			}
		}
		return true
	})
	return uses
}

func TestRenderPathAvoidsMathLibrary(t *testing.T) {
	root := repoRoot(t)
	found := map[string][]string{}
	scan := func(rel string) {
		found[rel] = inexactUses(t, filepath.Join(root, filepath.FromSlash(rel)))
	}
	for _, dir := range []string{"kernel", "render"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			scan(filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range guardedFiles {
		scan(rel)
	}
	var files []string
	for file := range found {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		if _, ok := notRenderPath[file]; ok {
			continue
		}
		got, want := len(found[file]), mathAllowlist[file]
		switch {
		case got > want:
			t.Errorf("%s uses math.%s, whose last bits differ by target (%d uses, allowed %d); use detmath or an exact formulation", file, strings.Join(found[file], ", math."), got, want)
		case got < want:
			t.Errorf("%s now makes %d inexact math uses; lower its mathAllowlist entry from %d", file, got, want)
		}
	}
	for file := range mathAllowlist {
		if _, ok := found[file]; !ok {
			t.Errorf("mathAllowlist names %s, which the scan does not cover", file)
		}
	}
	for file := range notRenderPath {
		if _, ok := found[file]; !ok {
			t.Errorf("notRenderPath names %s, which the scan does not cover", file)
		}
	}
}

// nearSites lists every use of a second-tier (...Near) function: per-sample
// nonlinearities on the audio path, each pinned by its processor's digest
// (package comment). A use anywhere else, or a different count, fails.
var nearSites = map[string]int{
	"kernel/fx/ampcab/ampcab.go": 4, // saturation per oversampled sample (TestAmpcabDigest)
	"kernel/fx/drive.go":         1, // fold shape per oversampled sample (TestDriveDigest)
	"kernel/fx/reverb.go":        1, // modulation per frame, also the room's (TestReverbDigest, TestRoomDigest)
}

func TestNearTierOnlyAtListedSites(t *testing.T) {
	root := repoRoot(t)
	found := map[string]int{}
	skip := map[string]bool{".git": true, "build": true, "node_modules": true, ".recovery": true, "testdata": true}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if n := nearUses(t, path); n > 0 {
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for file, n := range found {
		if want := nearSites[file]; n != want {
			t.Errorf("%s uses a ...Near function %d times (listed %d): the second tier is for the listed per-sample sites only", file, n, want)
		}
	}
	for file, want := range nearSites {
		if found[file] != want {
			t.Errorf("nearSites lists %s with %d uses, found %d", file, want, found[file])
		}
	}
}

// nearUses counts selectors detmath.XxxNear in a Go file that imports this package.
func nearUses(t *testing.T, path string) int {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution|parser.ImportsOnly)
	if err != nil {
		return 0 // not a parsable Go file (for example a generated fragment); the build would fail first
	}
	name := ""
	for _, spec := range file.Imports {
		if p, _ := strconv.Unquote(spec.Path.Value); p == "m31labs.dev/cicada/kernel/dsp/detmath" {
			name = "detmath"
			if spec.Name != nil {
				name = spec.Name.Name
			}
		}
	}
	if name == "" {
		return 0
	}
	file, err = parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if s, ok := node.(*ast.SelectorExpr); ok {
			if x, ok := s.X.(*ast.Ident); ok && x.Name == name && strings.HasSuffix(s.Sel.Name, "Near") {
				n++
			}
		}
		return true
	})
	return n
}
