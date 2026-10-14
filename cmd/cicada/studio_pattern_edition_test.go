package main

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

func TestPatternIntentInheritedEditionWithoutHeaderPrefix(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := []byte(strings.ReplaceAll("// authored header\n"+strings.Replace(studioPatternScore, "track bass acid {}", "track bass acid {}\ntrack input audio {}", 1), "\n", newline))
		path := mixerProject(t, "project p\ncicada 2\n", string(source))
		result, err := edits.Apply(source, edits.Envelope{Version: 1, Intents: []edits.Intent{&edits.SetPatternSettings{Entity: "pattern:p", Swing100: 5500, Gate: 60}}}, edits.Options{Edition: 2, Compiler: &studioCompiler{path: path}})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%q", result.Source)
		if bytes.Contains(result.Source, []byte("cicada 2")) || !bytes.HasPrefix(result.Source, []byte("// authored header"+newline)) {
			t.Fatalf("inherited edition bytes %q", result.Source)
		}
	}
}
