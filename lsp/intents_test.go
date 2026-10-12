package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/rendercheck"
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

// actionFrames runs Serve over one document and the given code-action requests,
// and returns the action lists in request order.
func actionFrames(t *testing.T, path, source string, requests ...map[string]any) [][]map[string]json.RawMessage {
	t.Helper()
	return actionFramesDocs(t, path, map[string]string{path: source}, requests...)
}

// actionFramesDocs opens every document in docs (path -> text) and sends the
// requests to the one at path.
func actionFramesDocs(t *testing.T, path string, docs map[string]string, requests ...map[string]any) [][]map[string]json.RawMessage {
	t.Helper()
	uri := fileURI(path)
	var input bytes.Buffer
	input.Write(rpc(t, 1, "initialize", map[string]any{"capabilities": map[string]any{"workspace": map[string]any{"workspaceEdit": map[string]any{"documentChanges": true}}}}))
	for name, text := range docs {
		input.Write(rpc(t, 0, "textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": fileURI(name), "version": 3, "text": text}}))
	}
	for i, request := range requests {
		request["textDocument"] = map[string]any{"uri": uri}
		input.Write(rpc(t, 2+i, "textDocument/codeAction", request))
	}
	input.Write(rpc(t, 0, "exit", map[string]any{}))
	var output bytes.Buffer
	if err := Serve(&input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	var results [][]map[string]json.RawMessage
	for {
		body, err := readFrame(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			ID     int                          `json:"id"`
			Result []map[string]json.RawMessage `json:"result"`
		}
		if json.Unmarshal(body, &frame) == nil && frame.ID >= 2 {
			results = append(results, frame.Result)
		}
	}
	if len(results) != len(requests) {
		t.Fatalf("got %d replies for %d requests", len(results), len(requests))
	}
	return results
}

// appliedEdit decodes an action's single document edit and applies it to source.
func appliedEdit(t *testing.T, action map[string]json.RawMessage, source string) (textEdit, string, int) {
	t.Helper()
	var edit struct {
		DocumentChanges []struct {
			TextDocument struct {
				Version int `json:"version"`
			} `json:"textDocument"`
			Edits []textEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(action["edit"], &edit); err != nil || len(edit.DocumentChanges) != 1 || len(edit.DocumentChanges[0].Edits) != 1 {
		t.Fatalf("edit: %s, %v", action["edit"], err)
	}
	change := edit.DocumentChanges[0].Edits[0]
	start, end := byteOffset([]byte(source), change.Range.Start), byteOffset([]byte(source), change.Range.End)
	return change, source[:start] + change.NewText + source[end:], edit.DocumentChanges[0].TextDocument.Version
}

func actionTitle(action map[string]json.RawMessage) (string, string) {
	var title, kind string
	_ = json.Unmarshal(action["title"], &title)
	_ = json.Unmarshal(action["kind"], &kind)
	return title, kind
}

const offGridScore = "cicada 2\ntrack bass acid {}\npattern hits { c3 . c4 . }\narrange {\n  place a bass hits { at = 100ticks length = 1bar }\n}\n"

func TestCodeActionSnapsOffGridPlacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grid.cicada")
	diagnostic := map[string]any{"range": region{Start: position{Line: 4}, End: position{Line: 4, Character: 1}}, "code": "CICADA-ARRANGEMENT", "message": "step pattern placement must start on a step"}
	request := func(only ...string) map[string]any {
		context := map[string]any{"diagnostics": []any{diagnostic}}
		if len(only) > 0 {
			context["only"] = only
		}
		return map[string]any{"range": region{Start: position{Line: 4}, End: position{Line: 4}}, "context": context}
	}
	replies := actionFrames(t, path, offGridScore, request(), request("quickfix"), request("refactor"),
		map[string]any{"range": region{}, "context": map[string]any{"diagnostics": []any{}}})
	for i, name := range []string{"no filter", "quickfix"} {
		if len(replies[i]) != 1 {
			t.Fatalf("%s: %d actions", name, len(replies[i]))
		}
		title, kind := actionTitle(replies[i][0])
		change, applied, version := appliedEdit(t, replies[i][0], offGridScore)
		if title != "Snap placement to the step grid" || kind != "quickfix" || version != 3 {
			t.Fatalf("%s: %q %q v%d", name, title, kind, version)
		}
		// The edit replaces exactly the offending literal.
		if change.NewText != "@1.1.1" || change.Range != (region{Start: position{Line: 4, Character: 27}, End: position{Line: 4, Character: 35}}) {
			t.Fatalf("%s: edit %+v", name, change)
		}
		if want := strings.Replace(offGridScore, "100ticks", "@1.1.1", 1); applied != want {
			t.Fatalf("%s: applied\n%s", name, applied)
		}
	}
	if len(replies[2]) != 0 || len(replies[3]) != 0 {
		t.Fatalf("refactor filter or empty diagnostics offered an action: %v %v", replies[2], replies[3])
	}
}

type fakeRender struct {
	mu     sync.Mutex
	calls  int
	differ bool
}

func (f *fakeRender) Hash(source []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.differ && bytes.Contains(source, []byte("arrange {")) {
		return "converted", nil
	}
	return "song", nil
}

const lspSongScore = "title \"Song\"\ntrack bass acid {}\npattern hits { c3 . c4 . }\nscene main { bass = hits }\nscene break { bass = hits }\nsong { main*2 break*2 }\n"

func withRenderCheck(t *testing.T, fake edits.RenderCheck) {
	t.Helper()
	previous := newRenderCheck
	newRenderCheck = func(string, map[string][]byte) edits.RenderCheck { return fake }
	t.Cleanup(func() { newRenderCheck = previous })
}

func TestCodeActionOffersSongConversionAfterTheCheckAndCachesIt(t *testing.T) {
	fake := &fakeRender{}
	withRenderCheck(t, fake)
	inSong := map[string]any{"range": region{Start: position{Line: 5}, End: position{Line: 5}}, "context": map[string]any{"diagnostics": []any{}}}
	again := map[string]any{"range": region{Start: position{Line: 5, Character: 3}, End: position{Line: 5, Character: 3}}, "context": map[string]any{"diagnostics": []any{}, "only": []string{"refactor"}}}
	outside := map[string]any{"range": region{Start: position{Line: 1}, End: position{Line: 1}}, "context": map[string]any{"diagnostics": []any{}}}
	replies := actionFrames(t, filepath.Join(t.TempDir(), "song.cicada"), lspSongScore, inSong, again, outside)
	for i := 0; i < 2; i++ {
		if len(replies[i]) != 1 {
			t.Fatalf("request %d: %d actions", i, len(replies[i]))
		}
		title, kind := actionTitle(replies[i][0])
		_, applied, version := appliedEdit(t, replies[i][0], lspSongScore)
		want := "cicada 2\n" + strings.Replace(lspSongScore, "song { main*2 break*2 }", "arrange {\n  place main-1-bass bass hits {\n    at = @1.1.1 length = 4bars\n  }\n}", 1)
		if title != "Convert song to arrange" || kind != "refactor" || version != 3 || applied != want {
			t.Fatalf("request %d: %q %q v%d\n%s", i, title, kind, version, applied)
		}
	}
	// A cursor outside the song block never starts a render.
	if len(replies[2]) != 0 {
		t.Fatalf("outside the song: %v", replies[2])
	}
	// The song and its conversion render once each across both requests.
	if fake.calls != 2 {
		t.Fatalf("render calls=%d, want 2", fake.calls)
	}
}

func TestCodeActionHidesSongConversionWhenTheCheckFailsOrIsMissing(t *testing.T) {
	inSong := func() map[string]any {
		return map[string]any{"range": region{Start: position{Line: 5}, End: position{Line: 5}}, "context": map[string]any{"diagnostics": []any{}}}
	}
	for name, check := range map[string]edits.RenderCheck{"differs": &fakeRender{differ: true}, "none": nil} {
		t.Run(name, func(t *testing.T) {
			withRenderCheck(t, check)
			replies := actionFrames(t, filepath.Join(t.TempDir(), "song.cicada"), lspSongScore, inSong())
			if len(replies[0]) != 0 {
				t.Fatalf("offered: %v", replies[0])
			}
		})
	}
}

func writePeerProject(t *testing.T, entry, part string) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"cicada.mod": "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n", "main.cicada": entry, "part.cicada": part} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.cicada")
}

func TestCodeActionSnapsPlacementWhoseDefinitionsLiveInAPeerFile(t *testing.T) {
	entry := "arrange {\n  place a bass hits { at = 100ticks length = 1bar }\n}\n"
	path := writePeerProject(t, entry, "track bass acid {}\npattern hits { c3 . c4 . }\n")
	diagnostic := map[string]any{"range": region{Start: position{Line: 1}, End: position{Line: 1, Character: 1}}, "code": "CICADA-ARRANGEMENT", "message": "step pattern placement must start on a step"}
	replies := actionFrames(t, path, entry, map[string]any{"range": region{Start: position{Line: 1}, End: position{Line: 1}}, "context": map[string]any{"diagnostics": []any{diagnostic}}})
	if len(replies[0]) != 1 {
		t.Fatalf("%d actions", len(replies[0]))
	}
	_, applied, _ := appliedEdit(t, replies[0][0], entry)
	if want := strings.Replace(entry, "100ticks", "@1.1.1", 1); applied != want {
		t.Fatalf("applied:\n%s", applied)
	}
}

func TestRenderCacheMissesAfterAPeerFileChanges(t *testing.T) {
	entry := "scene a { bass = hits }\nsong { a*1 }\n"
	path := writePeerProject(t, entry, "track bass acid {}\npattern hits { c3 . c4 . }\n")
	fake := &fakeRender{}
	withRenderCheck(t, fake)
	s := &server{}
	check := s.renderCheck(path, nil)
	for range 2 {
		if _, err := check.Hash([]byte(entry)); err != nil {
			t.Fatal(err)
		}
	}
	if fake.calls != 1 {
		t.Fatalf("unchanged inputs rendered %d times, want 1", fake.calls)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "part.cicada"), []byte("track bass acid {}\npattern hits { c3 . c4 c3 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := check.Hash([]byte(entry)); err != nil || fake.calls != 2 {
		t.Fatalf("peer edit: err=%v renders=%d, want 2", err, fake.calls)
	}
}

func peerDrums(steps string) string {
	return "track bass acid {}\npattern beat acid {\n  " + steps + "\n}\npattern fill acid {\n  " + steps + "\n}\n"
}

// The decision follows the open buffers, not the saved files: conversion is
// offered only when the project as edited renders the same as its conversion.
func TestSongConversionFollowsUnsavedPeerBuffers(t *testing.T) {
	const silent, audible = "1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 .", "1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50"
	entry := "scene a { bass = beat }\nscene b { bass = fill }\nsong { a*2 b*2 }\n"
	for _, c := range []struct {
		name, disk, buffer string
		offered            bool
	}{
		{"audible unsaved peer over silent disk", silent, audible, false},
		{"silent unsaved peer over audible disk", audible, silent, true},
		{"peer saved to match: audible", audible, audible, false},
		{"peer saved to match: silent", silent, silent, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := writePeerProject(t, entry, peerDrums(c.disk))
			part := filepath.Join(filepath.Dir(path), "part.cicada")
			request := map[string]any{"range": region{Start: position{Line: 2}, End: position{Line: 2}}, "context": map[string]any{"diagnostics": []any{}, "only": []string{"refactor"}}}
			replies := actionFramesDocs(t, path, map[string]string{path: entry, part: peerDrums(c.buffer)}, request)
			if offered := len(replies[0]) == 1; offered != c.offered {
				t.Fatalf("offered=%v, want %v: %v", offered, c.offered, replies[0])
			}
		})
	}
}

func TestRenderCacheKeyFollowsOpenPeerBuffers(t *testing.T) {
	entry := "scene a { bass = beat }\nsong { a*1 }\n"
	path := writePeerProject(t, entry, peerDrums("1?50 . 5?50 1?50"))
	part := filepath.Join(filepath.Dir(path), "part.cicada")
	fake := &fakeRender{}
	withRenderCheck(t, fake)
	s := &server{}
	hash := func(buffer string) {
		t.Helper()
		buffers := map[string][]byte{path: []byte(entry), part: []byte(buffer)}
		if _, err := s.renderCheck(path, buffers).Hash([]byte(entry)); err != nil {
			t.Fatal(err)
		}
	}
	hash(peerDrums("1?50 . 5?50 1?50"))
	hash(peerDrums("1?50 . 5?50 1?50"))
	if fake.calls != 1 {
		t.Fatalf("same buffers rendered %d times, want 1", fake.calls)
	}
	hash(peerDrums("1 . 5 ."))
	if fake.calls != 2 {
		t.Fatalf("changed open peer buffer rendered %d times in total, want 2", fake.calls)
	}
}

func TestRenderCacheKeyFollowsFilesStagedByABatch(t *testing.T) {
	entry := "scene a { bass = hits }\nsong { a*1 }\n"
	path := writePeerProject(t, entry, "track bass acid {}\npattern hits { c3 . c4 . }\n")
	manifest := filepath.Join(filepath.Dir(path), "cicada.mod")
	fake := &fakeRender{}
	withRenderCheck(t, fake)
	check := (&server{}).renderCheck(path, nil)
	for _, staged := range []string{"project peers\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n", "project peers\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n", "project other\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n"} {
		if _, err := check.HashWith([]byte(entry), map[string][]byte{manifest: []byte(staged)}); err != nil {
			t.Fatal(err)
		}
	}
	if fake.calls != 2 {
		t.Fatalf("renders=%d, want 2 (a repeated staged state hits, a changed one misses)", fake.calls)
	}
}

// A peer rewritten during the render and restored leaves the disk as it was, so
// before/after identity checks cannot see it. The renders read one snapshot, so
// the LSP never offers a conversion that a fresh comparison of the project
// refuses, and nothing from the first request is served to the second.
func TestPeerRewrittenAndRestoredDuringTheRenderOffersNoConversionInTheLSP(t *testing.T) {
	const chance = "1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50 1?50 . 5?50 1?50"
	entry := "scene a { bass = beat }\nscene b { bass = fill }\nsong { a*2 b*2 }\n"
	path := writePeerProject(t, entry, peerDrums(chance))
	peer := filepath.Join(filepath.Dir(path), "part.cicada")
	rendercheck.AfterSnapshot = func() {
		_ = os.WriteFile(peer, []byte(peerDrums("1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 .")), 0o600)
	}
	rendercheck.AfterRender = func() { _ = os.WriteFile(peer, []byte(peerDrums(chance)), 0o600) }
	t.Cleanup(func() { rendercheck.AfterSnapshot, rendercheck.AfterRender = nil, nil })
	request := func() map[string]any {
		return map[string]any{"range": region{Start: position{Line: 2}, End: position{Line: 2}}, "context": map[string]any{"diagnostics": []any{}, "only": []string{"refactor"}}}
	}
	replies := actionFrames(t, path, entry, request(), request())
	if len(replies[0]) != 0 || len(replies[1]) != 0 {
		t.Fatalf("offered a conversion that a fresh comparison refuses: %v %v", replies[0], replies[1])
	}
}
