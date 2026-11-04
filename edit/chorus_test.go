package edit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChorusMixerCreateAndEdit(t *testing.T) {
	source, _, _, _, err := mixerSource([]byte(mixerScore), "fx.wide", json.RawMessage(`"chorus"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ name, value string }{{"voices", "2"}, {"rate", "1.7"}, {"depth", "4"}, {"spread", "0.4"}, {"mix", "0.7"}} {
		source, _, _, _, err = mixerSource(source, "wide."+v.name, json.RawMessage(v.value))
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
	}
	for _, text := range []string{"fx wide chorus", "voices = 2", "rate = 1.7Hz", "depth = 4ms", "spread = 0.4", "mix = 0.7"} {
		if !strings.Contains(string(source), text) {
			t.Fatalf("missing %q: %s", text, source)
		}
	}
	if _, _, _, _, err = mixerSource(source, "wide.voices", json.RawMessage(`2.4`)); err == nil {
		t.Fatal("accepted fractional chorus voices")
	}
}
