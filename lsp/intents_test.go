package lsp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
)

func TestLSPCompilerReturnsNamedPlacementPlan(t *testing.T) {
	path, err := filepath.Abs("../examples/arrangement/named.cicada")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (lspCompiler{path: path}).Compile(source, nil)
	if err != nil || plan.Revision != edits.Revision(source) || len(plan.Placements) != 2 || plan.Placements[1].ID != "return" || plan.Placements[1].AtTick != 2*3840 {
		t.Fatalf("plan=%+v, err=%v", plan, err)
	}
}

func TestLSPCompilerUsesUnsavedSourceAndAuxiliaryOverrides(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.cicada")
	part := filepath.Join(root, "part.cicada")
	for name, source := range map[string]string{"cicada.mod": "project edit\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n", "main.cicada": "scene main { bass=pulse }\nsong { main }\n", "part.cicada": "track bass acid {}\npattern pulse { 1 . }\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := []byte("title \"Unsaved\"\nscene main { bass=pulse }\nsong { main }\n")
	files := map[string][]byte{part: []byte("preset unused { instrument=acid cutoff=900Hz }\ntrack bass acid {}\npattern pulse { 5 . 1 . }\n")}
	original := bytes.Clone(files[part])
	plan, err := (lspCompiler{path: path}).Compile(source, files)
	if err != nil || plan.Title != "Unsaved" || plan.Revision != edits.Revision(source) || plan.Patterns[0].Steps != 4 || !plan.Names["unused"] || !bytes.Equal(files[part], original) || len(files) != 1 {
		t.Fatalf("override plan=%+v, err=%v, files=%v", plan, err, files)
	}
	if got, _ := os.ReadFile(path); bytes.Contains(got, []byte("Unsaved")) {
		t.Fatal("compiler wrote unsaved source")
	}
	if _, err := (lspCompiler{path: path}).Compile([]byte("???"), files); err == nil {
		t.Fatal("invalid source compiled")
	}
	files[part] = []byte("???")
	if _, err := (lspCompiler{path: path}).Compile(source, files); err == nil {
		t.Fatal("invalid auxiliary source compiled")
	}
}

type countingRenderCheck struct {
	calls int
	err   error
}

func (c *countingRenderCheck) Hash(source []byte) (string, error) {
	c.calls++
	return edits.Revision(source), c.err
}

func TestCachedCheckMemoizesExactRevisionsAndErrors(t *testing.T) {
	for _, failure := range []error{nil, errors.New("render failed")} {
		inner := &countingRenderCheck{err: failure}
		cache := &cachedCheck{inner: inner}
		for _, source := range [][]byte{[]byte("source\n"), []byte("source\n"), []byte("source\r\n"), []byte("source\n")} {
			hash, err := cache.Hash(source)
			if hash != edits.Revision(source) || err != failure {
				t.Fatalf("hash=%q, err=%v", hash, err)
			}
		}
		if inner.calls != 2 {
			t.Fatalf("render calls=%d, want 2", inner.calls)
		}
	}
	if _, err := (&cachedCheck{}).Hash([]byte("x")); !errors.Is(err, edits.ErrNoRenderCheck) {
		t.Fatalf("missing render check: %v", err)
	}
}

func TestCachedCheckConcurrentSameRevision(t *testing.T) {
	inner := &countingRenderCheck{}
	cache := &cachedCheck{inner: inner}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.Hash([]byte("same")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if inner.calls != 1 {
		t.Fatalf("render calls=%d", inner.calls)
	}
}

func TestMinimalTextEditRoundTripsAndUsesUTF16Ranges(t *testing.T) {
	for _, c := range []struct {
		before, after string
		want          region
		text          string
	}{
		{"abc", "axc", region{position{0, 1}, position{0, 2}}, "x"},
		{"abc", "abXYc", region{position{0, 2}, position{0, 2}}, "XY"},
		{"abc", "ac", region{position{0, 1}, position{0, 2}}, ""},
		{"abc", "abc", region{position{0, 3}, position{0, 3}}, ""},
		{"", "😀", region{}, "😀"},
		{"😀", "", region{position{}, position{0, 2}}, ""},
		{"// 😀\nabc\n", "// 😀\naxc\n", region{position{1, 1}, position{1, 2}}, "x"},
		{"😀x", "😁x", region{position{}, position{0, 2}}, "😁"},
		{"éx", "êx", region{position{}, position{0, 1}}, "ê"},
		{"xé", "xê", region{position{0, 1}, position{0, 2}}, "ê"},
		{"a\r\nb\r\n", "a\r\nB\r\n", region{position{1, 0}, position{1, 1}}, "B"},
		{"a\r\nb", "a\nb", region{position{0, 1}, position{1, 0}}, "\n"},
		{"a\nb", "a\r\nb", region{position{0, 1}, position{1, 0}}, "\r\n"},
	} {
		t.Run(c.before+"->"+c.after, func(t *testing.T) {
			before := []byte(c.before)
			change := minimalTextEdit(before, []byte(c.after))
			if !reflect.DeepEqual(change.Range, c.want) || change.NewText != c.text || !utf8.ValidString(change.NewText) {
				t.Fatalf("edit=%+v, want range=%+v text=%q", change, c.want, c.text)
			}
			start, end := byteOffset(before, change.Range.Start), byteOffset(before, change.Range.End)
			got := c.before[:start] + change.NewText + c.before[end:]
			if got != c.after {
				t.Fatalf("applied=%q, want=%q", got, c.after)
			}
		})
	}
}
