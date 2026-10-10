package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"m31labs.dev/cicada/notation"
)

// changedWindow returns after minus the longest common prefix and suffix with
// before: the bytes an edit actually inserted or replaced.
func changedWindow(before, after []byte) []byte {
	prefix := 0
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(after)-prefix && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	return after[prefix : len(after)-suffix]
}

// The rule-6 fixture uses untransposed phrases. Only step counts are needed
// from this injected plan; Studio's writer parity tests use a full compiler.
type sharedFixtureCompiler struct{}

func (sharedFixtureCompiler) Compile(source []byte, files map[string][]byte) (*Plan, error) {
	plan, err := (parseCompiler{}).Compile(source, files)
	if err != nil {
		return nil, err
	}
	score, _ := notation.Parse(source)
	for _, p := range score.Patterns {
		plan.Patterns = append(plan.Patterns, Pattern{ID: p.Name, Data: make([]*Step, len(p.Steps))})
	}
	return plan, nil
}

func TestFidelityRule6_SharedPhraseAsks(t *testing.T) {
	fixture := []byte("track bass acid {}\nphrase riff { 1 . 5 . }\npattern pulse acid { use riff*2 }\nscene main { bass=pulse }\nsong { main }\n")
	apply := func(source []byte, in *ToggleStep) (*Result, error) {
		return Apply(source, Envelope{Version: 1, Intents: []Intent{in}}, Options{Compiler: sharedFixtureCompiler{}})
	}
	for _, index := range []int{0, 4} {
		_, err := apply(fixture, &ToggleStep{Entity: EntityID("step:pulse/" + strconv.Itoa(index))})
		var shared *SharedPhraseError
		if !errors.As(err, &shared) || shared.Pattern != "pulse" || shared.Phrase != "riff" || shared.Step != index {
			t.Fatalf("shared origin: %+v (%v)", shared, err)
		}
	}
	for _, policy := range []string{"definition", "detach", "pattern"} {
		got, err := apply(fixture, &ToggleStep{Entity: "step:pulse/0", Shared: policy})
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Replace(fixture, []byte("phrase riff { 1 . 5 . }"), []byte("phrase riff { . . 5 . }"), 1)
		if policy == "detach" || policy == "pattern" {
			want = bytes.Replace(fixture, []byte("use riff*2"), []byte(". . 5 . 1 . 5 ."), 1)
		}
		if !bytes.Equal(got.Source, want) {
			t.Fatalf("%s bytes %q, want %q", policy, got.Source, want)
		}
	}
	direct := bytes.Replace(fixture, []byte("use riff*2"), []byte("1 . 5 . use riff"), 1)
	for _, policy := range []string{"", "definition", "detach", "ignored"} {
		got, err := apply(direct, &ToggleStep{Entity: "step:pulse/0", Shared: policy})
		want := bytes.Replace(direct, []byte("{ 1 . 5 . use riff }"), []byte("{ . . 5 . use riff }"), 1)
		if err != nil || !bytes.Equal(got.Source, want) {
			t.Fatalf("direct step policy %q: %+v, %v", policy, got, err)
		}
	}
	mixed := bytes.Replace(fixture, []byte("use riff*2"), []byte("1 . use riff"), 1)
	got, err := apply(mixed, &ToggleStep{Entity: "step:pulse/0", Shared: "pattern"})
	want := bytes.Replace(mixed, []byte("{ 1 . use riff }"), []byte("{ . . 1 . 5 . }"), 1)
	if err != nil || !bytes.Equal(got.Source, want) {
		t.Fatalf("whole mixed pattern: %+v, %v", got, err)
	}
	twoUses := bytes.Replace(fixture, []byte("use riff*2"), []byte("use riff use riff"), 1)
	got, err = apply(twoUses, &ToggleStep{Entity: "step:pulse/4", Shared: "detach"})
	want = bytes.Replace(twoUses, []byte("use riff use riff"), []byte("use riff | . . 5 ."), 1)
	if err != nil || !bytes.Equal(got.Source, want) {
		t.Fatalf("single use: %+v, %v", got, err)
	}
}

// Rule 2: an edit changes only the bytes it must. Comments, other
// declarations and the offsets of untouched text stay exactly where they were.
func TestFidelityRule2_SetParamTouchesOnlyItsWindow(t *testing.T) {
	fixture, param := setParamFixture(t)
	apply := func(source []byte, value string) []byte {
		t.Helper()
		result, err := Apply(source, Envelope{Version: 1, Intents: []Intent{&SetParam{Entity: EntityID("param:keys." + param), Value: json.RawMessage(value)}}}, Options{Compiler: parseCompiler{}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Source
	}
	first := apply(fixture, `"0.5"`)
	window := changedWindow(fixture, first)
	if want := " " + param + " = 0.5"; !bytes.HasPrefix(window, []byte(want)) {
		t.Fatalf("window %q does not start with %q", window, want)
	}
	insertAt := bytes.Index(fixture, []byte("track keys my-pad {")) + len("track keys my-pad {")
	if !bytes.Equal(first[:insertAt], fixture[:insertAt]) || !bytes.Equal(first[len(first)-(len(fixture)-insertAt):], fixture[insertAt:]) {
		t.Fatal("bytes outside the insertion changed")
	}
	second := apply(first, `"0.25"`)
	if want := bytes.Replace(first, []byte(" "+param+" = 0.5"), []byte(" "+param+" = 0.25"), 1); !bytes.Equal(second, want) {
		t.Fatalf("second edit changed more than the literal:\n%s", second)
	}
	for _, anchor := range []string{"// authored track", "title \"Keep my title\""} {
		before, after := bytes.Index(fixture, []byte(anchor)), bytes.Index(first, []byte(anchor))
		if before < 0 || before != after {
			t.Fatalf("%q moved from %d to %d", anchor, before, after)
		}
	}
	// Text after the insertion shifts by exactly the inserted length.
	if tail := "song { main*2 }"; bytes.Index(first, []byte(tail))-bytes.Index(fixture, []byte(tail)) != len(first)-len(fixture) {
		t.Fatal("trailing text shifted by more than the insertion")
	}
}
