package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExperimentalGuitarParameters(t *testing.T) {
	source := []byte("cicada 2\ntrack lead guitar { experimental = on }\npattern p { 1 }\nscene main { lead = p lead.vibrato = 25 }\nsong { main }\n")
	offset := bytes.Index(source, []byte("lead.vibrato")) + 6
	value, _ := json.Marshal(hover(source, utf16Position(source, offset)))
	for _, want := range []string{"Type: number", "Unit: cent", "0..100cent", "Smoothing: 8 ms", "Live: true"} {
		if !strings.Contains(string(value), want) {
			t.Fatalf("hover missing %s: %s", want, value)
		}
	}
	partial := []byte("cicada 2\ntrack lead guitar { experimental = on }\nscene main { lead.\n}\n")
	items := parameterPathCompletion(partial, utf16Position(partial, bytes.Index(partial, []byte("lead."))+5))
	value, _ = json.Marshal(items)
	for _, want := range []string{"bend", "vibrato", "brightness", "damping", "pickup", "drive", "level"} {
		if !strings.Contains(string(value), want) {
			t.Fatalf("completion missing %s: %s", want, value)
		}
	}
}
