package seq

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A bar's length comes from a MeterMap. This test fails when the removed
// constant comes back, or when a literal 3840 appears in a file that is not
// listed below. Each entry is the exact number of literal 3840s a file has
// today and why it is not a bar length: a new one fails, and a removed one
// asks for the entry to be lowered. Test files, testdata and generated
// minified assets are not scanned.
var barLiteralAllowlist = map[string]struct {
	count  int
	reason string
}{
	// A whole note: the longest step cell, and the base of note values.
	"kernel/cmd/command.go": {2, "OpSeek's tick unit (SeekUnit) and the longest pattern cell; kernel/cmd cannot import seq"},
	"kernel/seq/step.go":    {1, "the longest pattern cell, a whole note"},
	"notation/grid.go":      {2, "note divisions are fractions of a whole note"},
	"project/validate.go":   {1, "the longest pattern cell, a whole note"},
	// A frame count, not ticks.
	"internal/audiobackend/tymbal_period_native.go": {1, "an audio buffer size in frames"},
	// The seek wire encoding: tick = Arg0*3840 + Arg1 (cmd.SeekUnit).
	"host/web/client.js":    {3, "seek arguments encode a tick in units of cmd.SeekUnit"},
	"host/web/processor.js": {2, "seek arguments encode a tick in units of cmd.SeekUnit; bar lines come from the kernel's next_bar"},
	// Page display that Studio's phase 5 moves to a meter map sent as display data.
	"cmd/cicada/studio-live.js":  {4, "live pads count 4/4 bars until pages receive the meter map (time-signatures phase 5)"},
	"cmd/cicada/view.html":       {2, "the transport label counts 4/4 bars until pages receive the meter map (phase 5)"},
	"host/demobrowser/page.js":   {1, "the demo transport label counts 4/4 bars (phase 5)"},
	"host/web/capture-client.js": {1, "the recording count-in is 4/4 until the count-in follows the meter (phase 5)"},
}

var jsBarLiteral = regexp.MustCompile(`\b3_?840\b`)

func barGuardRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found from %s: %v", root, err)
	}
	return root
}

func scannedSource(rel string, d fs.DirEntry) (skipDir, scan bool) {
	name := d.Name()
	if d.IsDir() {
		return name == "build" || name == "testdata" || name == "node_modules" || strings.HasPrefix(name, "."), false
	}
	if strings.HasSuffix(name, "_test.go") || strings.Contains(name, ".test.") || strings.HasSuffix(name, "_test.cjs") || name == "test.cjs" || strings.HasSuffix(name, ".min.js") {
		return false, false
	}
	switch filepath.Ext(name) {
	case ".go", ".js", ".cjs", ".mjs", ".html":
		return false, true
	}
	return false, false
}

func TestNoFixedBarLength(t *testing.T) {
	root := barGuardRoot(t)
	found := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		skipDir, scan := scannedSource(rel, d)
		if skipDir && path != root {
			return filepath.SkipDir
		}
		if !scan {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(rel, ".go") {
			if n := len(jsBarLiteral.FindAll(data, -1)); n > 0 {
				found[rel] = n
			}
			return nil
		}
		file, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
		if err != nil {
			return nil // a file that does not parse fails its own build
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BasicLit:
				if v.Kind == token.INT && (v.Value == "3840" || v.Value == "3_840") {
					found[rel]++
				}
			case *ast.SelectorExpr:
				if x, ok := v.X.(*ast.Ident); ok && x.Name == "seq" && v.Sel.Name == "TicksPerBar" {
					t.Errorf("%s uses seq.TicksPerBar; ask a MeterMap for bar lengths", rel)
				}
			case *ast.ValueSpec:
				if strings.HasPrefix(rel, "kernel/seq/") {
					for _, name := range v.Names {
						if name.Name == "TicksPerBar" {
							t.Errorf("%s declares TicksPerBar again; a bar's length comes from a MeterMap", rel)
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for file := range found {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		allowed, ok := barLiteralAllowlist[file]
		switch {
		case !ok:
			t.Errorf("%s has %d literal 3840s; a bar's length comes from a MeterMap (a whole note is seq.TicksPerWhole, a seek unit cmd.SeekUnit)", file, found[file])
		case found[file] > allowed.count:
			t.Errorf("%s has %d literal 3840s, %d allowed (%s)", file, found[file], allowed.count, allowed.reason)
		case found[file] < allowed.count:
			t.Errorf("%s has %d literal 3840s; lower its allowance from %d", file, found[file], allowed.count)
		}
	}
	for file, allowed := range barLiteralAllowlist {
		if found[file] == 0 {
			t.Errorf("%s has no literal 3840 left; remove its allowance (%d)", file, allowed.count)
		}
	}
}
