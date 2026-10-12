package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// arrangeCompiler validates with the notation parser and builds the plan view
// the arrange handlers resolve against: placements, pattern lengths and slides.
type arrangeCompiler struct{ edition int }

func (c arrangeCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	parse := notation.Parse
	if c.edition != 0 {
		parse = func(src []byte) (*notation.Score, []notation.Diagnostic) {
			return notation.ParseEdition(src, c.edition)
		}
	}
	score, ds := parse(source)
	if hasErrors(ds) {
		var lines []string
		for _, d := range ds {
			if d.Severity == "error" {
				lines = append(lines, d.Message)
			}
		}
		return nil, errors.New(strings.Join(lines, "; "))
	}
	plan := &Plan{Revision: Revision(source), Names: map[string]bool{}}
	for _, pattern := range score.Patterns {
		out := Pattern{ID: pattern.Name, Kind: pattern.Kind, Steps: uint8(len(pattern.Steps))}
		for _, token := range pattern.Steps {
			out.Data = append(out.Data, &Step{Slide: strings.HasSuffix(token.Text, "~")})
		}
		plan.Patterns = append(plan.Patterns, out)
		plan.Names[pattern.Name] = true
	}
	for _, scene := range score.Scenes {
		plan.Scenes = append(plan.Scenes, Scene{ID: scene.Name})
		plan.Names[scene.Name] = true
	}
	if score.Arrange != nil {
		for _, p := range score.Arrange.Placements {
			plan.Placements = append(plan.Placements, Placement{ID: p.Name, Track: p.Track, Content: p.Content, AtTick: p.AtTick, LengthTicks: p.LengthTicks})
			plan.Names[p.Name] = true
		}
		for _, m := range score.Arrange.Markers {
			plan.Markers = append(plan.Markers, Marker{ID: m.Name, AtTick: m.AtTick})
			plan.Names[m.Name] = true
		}
	}
	return plan, nil
}

func namedScore(t *testing.T) []byte {
	t.Helper()
	source, err := os.ReadFile("../examples/arrangement/named.cicada")
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func applyArrange(source []byte, raw string, opts Options) (*Result, error) {
	if opts.Compiler == nil {
		opts.Compiler = arrangeCompiler{}
	}
	return applyM4Intent(source, raw, opts)
}

func TestFormatMusicalTime(t *testing.T) {
	for _, c := range []struct {
		tick             int64
		position, length string
	}{
		{0, "@1.1.1", "0ticks"},
		{3840, "@2.1.1", "1bar"},
		{5 * 3840, "@6.1.1", "5bars"},
		{960 + 240, "@1.2.2", "1200ticks"},
		{100, "100ticks", "100ticks"},
		{3840 + 250, "4090ticks", "4090ticks"},
		{2 * 3840, "@3.1.1", "2bars"},
	} {
		if got := FormatPosition(c.tick); got != c.position {
			t.Errorf("FormatPosition(%d)=%q, want %q", c.tick, got, c.position)
		}
		if got := FormatLength(c.tick); got != c.length {
			t.Errorf("FormatLength(%d)=%q, want %q", c.tick, got, c.length)
		}
	}
}

func TestMoveClipChangesOnlyAt(t *testing.T) {
	source := namedScore(t)
	got, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:intro","attick":19200}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(source, []byte("at = @1.2.1"), []byte("at = @6.1.1"), 1)
	if !bytes.Equal(got.Source, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Source, want)
	}
	if got.Label != "Clip intro moved to @6.1.1" {
		t.Fatalf("label %q", got.Label)
	}
	// An off-grid start is a compile error for a step pattern.
	if _, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:intro","attick":100}`, Options{}); err == nil || !strings.Contains(err.Error(), "step pattern placement must start on a step") {
		t.Fatalf("off-grid: %v", err)
	}
	// Bar 3 is occupied by return.
	if _, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:intro","attick":7680}`, Options{}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlap: %v", err)
	}
	if _, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:nope","attick":0}`, Options{}); err == nil || !strings.Contains(err.Error(), `unknown placement "nope"`) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := applyArrange(source, `{"kind":"moveclip","entity":"pattern:pulse","attick":0}`, Options{}); err == nil {
		t.Fatal("non-placement entity accepted")
	}
}

func TestResizeClipChangesOnlyLength(t *testing.T) {
	source := namedScore(t)
	got, err := applyArrange(source, `{"kind":"resizeclip","entity":"placement:return","lengthticks":3840}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(source, []byte("length = 2bars"), []byte("length = 1bar"), 1)
	if !bytes.Equal(got.Source, want) || got.Label != "Clip return resized to 1bar" {
		t.Fatalf("got %q / %q", got.Source, got.Label)
	}
	if _, err := applyArrange(source, `{"kind":"resizeclip","entity":"placement:return","lengthticks":0}`, Options{}); err == nil {
		t.Fatal("zero length accepted")
	}
}

type fakeCheck struct {
	hashes map[string]string // by substring of the source
	calls  int
}

func (f *fakeCheck) Hash(source []byte) (string, error) {
	f.calls++
	for needle, hash := range f.hashes {
		if bytes.Contains(source, []byte(needle)) {
			return hash, nil
		}
	}
	return "same", nil
}

func TestSplitClipAtPatternBoundary(t *testing.T) {
	source := namedScore(t)
	got, err := applyArrange(source, `{"kind":"splitclip","entity":"placement:return","attick":11520}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(source), "  place return bass pulse {\n    at = @3.1.1\n    length = 2bars\n  }\n",
		"  place return bass pulse {\n    at = @3.1.1\n    length = 1bar\n  }\n  place return-2 bass pulse {\n    at = @4.1.1\n    length = 1bar\n  }\n", 1)
	if string(got.Source) != want || got.Label != "Clip return split at @4.1.1" {
		t.Fatalf("got:\n%s\nlabel %q", got.Source, got.Label)
	}
	// A split on a pattern-length boundary that is not on a bar still works:
	// pulse is 960 ticks, so beat 2 of bar 3 is a boundary.
	if _, err := applyArrange(source, `{"kind":"splitclip","entity":"placement:return","attick":8640}`, Options{}); err != nil {
		t.Fatal(err)
	}
	var refusal *RefusalError
	_, err = applyArrange(source, `{"kind":"splitclip","entity":"placement:return","attick":8180}`, Options{})
	if !errors.As(err, &refusal) || refusal.Cause != "split point is not on a pattern-length boundary (pulse is 960 ticks)" {
		t.Fatalf("off boundary: %v", err)
	}
	for _, tick := range []string{"7680", "15360", "0"} {
		if _, err := applyArrange(source, `{"kind":"splitclip","entity":"placement:return","attick":`+tick+`}`, Options{}); !errors.As(err, &refusal) || refusal.Cause != "split point is outside the placement" {
			t.Fatalf("outside %s: %v", tick, err)
		}
	}
}

func TestSplitClipSlideRunsRenderCheck(t *testing.T) {
	source := bytes.Replace(namedScore(t), []byte("pattern pulse { c3 . c4~ c3 }"), []byte("pattern glide { c3 . c4 c3~ }"), 1)
	source = bytes.ReplaceAll(source, []byte("bass pulse"), []byte("bass glide"))
	raw := `{"kind":"splitclip","entity":"placement:return","attick":11520}`
	if _, err := applyArrange(source, raw, Options{}); !errors.Is(err, ErrNoRenderCheck) {
		t.Fatalf("nil check: %v", err)
	}
	var refusal *RefusalError
	differing := &fakeCheck{hashes: map[string]string{"return-2": "cut"}}
	if _, err := applyArrange(source, raw, Options{RenderCheck: differing}); !errors.As(err, &refusal) || refusal.Cause != "splitting glide at bar 4 cuts a slide" {
		t.Fatalf("differing: %v", err)
	}
	if got, err := applyArrange(source, raw, Options{RenderCheck: &fakeCheck{}}); err != nil || !bytes.Contains(got.Source, []byte("place return-2 bass glide")) {
		t.Fatalf("equal: %v", err)
	}
	// Without a slide the check is not consulted.
	plain := &fakeCheck{hashes: map[string]string{"return-2": "cut"}}
	if _, err := applyArrange(namedScore(t), raw, Options{RenderCheck: plain}); err != nil || plain.calls != 0 {
		t.Fatalf("plain: %v calls=%d", err, plain.calls)
	}
}

func TestDuplicateClip(t *testing.T) {
	source := namedScore(t)
	got, err := applyArrange(source, `{"kind":"duplicateclip","entity":"placement:return"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(source), "  marker chorus", "  place return-2 bass pulse {\n    at = @5.1.1\n    length = 2bars\n  }\n  marker chorus", 1)
	if string(got.Source) != want || got.Label != "Clip return duplicated as return-2" {
		t.Fatalf("got:\n%s\nlabel %q", got.Source, got.Label)
	}
	// intro ends at bar 2 beat 2, so the copy would overlap return.
	if _, err := applyArrange(source, `{"kind":"duplicateclip","entity":"placement:intro"}`, Options{}); err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlap: %v", err)
	}
	// A taken name is skipped.
	twice, err := applyArrange(got.Source, `{"kind":"duplicateclip","entity":"placement:return"}`, Options{})
	if err != nil || !bytes.Contains(twice.Source, []byte("place return-3 bass pulse")) {
		// the third copy lands on the second at bar 5, so it overlaps; the name is what is checked
		if err == nil || !strings.Contains(err.Error(), "overlapping") {
			t.Fatalf("second copy: %v", err)
		}
	}
}

func TestDeleteClipRemovesAttachedComments(t *testing.T) {
	source := []byte(strings.Replace(string(namedScore(t)), "  place intro", "  // lead-in\n  // keep short\n  place intro", 1))
	source = bytes.Replace(source, []byte("  place return"), []byte("\n  // standalone\n\n  // attached\n  place return"), 1)
	got, err := applyArrange(source, `{"kind":"deleteclip","entity":"placement:intro"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got.Source, []byte("lead-in")) || bytes.Contains(got.Source, []byte("place intro")) || !bytes.Contains(got.Source, []byte("// standalone")) || !bytes.Contains(got.Source, []byte("place return")) {
		t.Fatalf("intro delete:\n%s", got.Source)
	}
	got, err = applyArrange(source, `{"kind":"deleteclip","entity":"placement:return"}`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got.Source, []byte("// attached")) || !bytes.Contains(got.Source, []byte("// standalone")) || bytes.Contains(got.Source, []byte("place return")) {
		t.Fatalf("return delete:\n%s", got.Source)
	}
	// The last placement cannot be deleted.
	one := []byte("cicada 2\ntrack bass acid {}\npattern pulse { c3 . c4 c3 }\narrange {\n  place only bass pulse { at = @1.1.1 length = 1bar }\n}\n")
	if _, err := applyArrange(one, `{"kind":"deleteclip","entity":"placement:only"}`, Options{}); err == nil || !strings.Contains(err.Error(), "arrange needs at least one placement") {
		t.Fatalf("last placement: %v", err)
	}
}

func TestArrangeIntentsRoundTripJSON(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"moveclip","entity":"placement:a","attick":240}`,
		`{"kind":"resizeclip","entity":"placement:a","lengthticks":3840}`,
		`{"kind":"splitclip","entity":"placement:a","attick":3840}`,
		`{"kind":"duplicateclip","entity":"placement:a"}`,
		`{"kind":"deleteclip","entity":"placement:a"}`,
		`{"kind":"convertsongtoarrange"}`,
	} {
		intent, err := decodeIntent([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		encoded, err := encodeIntent(intent)
		var a, b any
		if err != nil || json.Unmarshal(encoded, &a) != nil || json.Unmarshal([]byte(raw), &b) != nil || !reflect.DeepEqual(a, b) {
			t.Fatalf("%s -> %s, %v", raw, encoded, err)
		}
	}
}

func TestMoveClipRepairsAScoreThatDoesNotCompile(t *testing.T) {
	source := bytes.Replace(namedScore(t), []byte("at = @1.2.1"), []byte("at = 100ticks"), 1)
	if _, err := (arrangeCompiler{}).Compile(source, nil); err == nil {
		t.Fatal("fixture compiles")
	}
	got, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:intro","attick":0}`, Options{})
	if err != nil || !bytes.Contains(got.Source, []byte("at = @1.1.1")) {
		t.Fatalf("got %v", err)
	}
	if _, err := applyArrange(source, `{"kind":"moveclip","entity":"placement:nope","attick":0}`, Options{}); err == nil {
		t.Fatal("unknown placement accepted")
	}
}
