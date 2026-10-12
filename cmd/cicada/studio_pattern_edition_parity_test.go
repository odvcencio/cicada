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
		body := studioEdit{Action: "settings", Pattern: "p", Settings: &studioPatternSettings{Swing100: 5500, Gate: 60}}
		want, err := patternSettingsSource(source, "p", body.Settings)
		if err != nil {
			t.Fatal(err)
		}
		got, err := applyParityIntent(t, source, patternIntentJSON(body), edits.Options{Edition: 2, Compiler: &studioCompiler{path: path}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Source, want) || bytes.Contains(got.Source, []byte("cicada 2")) {
			t.Fatalf("inherited edition bytes %q, want %q", got.Source, want)
		}
	}
}
