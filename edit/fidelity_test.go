package edit

import (
	"bytes"
	"encoding/json"
	"testing"
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
