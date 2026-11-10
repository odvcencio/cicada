package detmath

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The render path must not call the math library functions this package
// replaces: their last bits differ by target. This test scans the master
// chain (kernel/fx and its subpackages, kernel/mix, kernel/loudness) and the
// export code (render/wav.go and render/verify.go for the export gain and its
// measurements, cmd/cicada/main.go for the loudness search). Test files are
// not scanned.
//
// The files in mathAllowlist still call the math library because they are
// linked into the core WebAssembly kernel, whose size budget cannot also hold
// this package. Each entry is the exact number of calls the file makes today:
// a new call fails, and a removed one asks for the entry to be lowered.

var mathAllowlist = map[string]int{
	"kernel/fx/ampcab/ampcab.go": 11,
	"kernel/fx/chorus/chorus.go": 2,
	"kernel/fx/comp.go":          5,
	"kernel/fx/deesser.go":       5,
	"kernel/fx/delay.go":         2,
	"kernel/fx/drive.go":         6,
	"kernel/fx/reverb.go":        5,
	"kernel/fx/tape/tape.go":     2,
	"kernel/mix/mix.go":          6,
}

var replacedCall = regexp.MustCompile(`\bmath\.(Sin|Cos|Sincos|Tan|Pow|Exp|Exp2|Log|Log2|Log10|Tanh)\(`)

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

// countCalls returns the replaced math calls in a Go file, ignoring comments.
func countCalls(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		n += len(replacedCall.FindAllString(line, -1))
	}
	return n
}

func TestRenderPathAvoidsMathLibrary(t *testing.T) {
	root := repoRoot(t)
	found := map[string]int{}
	for _, dir := range []string{"kernel/fx", "kernel/mix", "kernel/loudness"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			found[filepath.ToSlash(rel)] = countCalls(t, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"render/wav.go", "render/verify.go", "cmd/cicada/main.go"} {
		found[file] = countCalls(t, filepath.Join(root, file))
	}
	var files []string
	for file := range found {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		got, want := found[file], mathAllowlist[file]
		switch {
		case got > want:
			t.Errorf("%s calls a math function that detmath replaces %d times (allowed %d); use detmath", file, got, want)
		case got < want:
			t.Errorf("%s now makes %d replaced math calls; lower its mathAllowlist entry from %d", file, got, want)
		}
	}
	for file := range mathAllowlist {
		if _, ok := found[file]; !ok {
			t.Errorf("mathAllowlist names %s, which the scan does not cover", file)
		}
	}
}
