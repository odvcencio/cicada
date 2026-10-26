package edit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAmpCabMixerWrites(t *testing.T) {
	source := []byte(mixerScore)
	var err error
	source, _, _, _, err = mixerSource(source, "fx.rig", json.RawMessage(`{"kind":"ampcab"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ name, value string }{{"drive", "0.7"}, {"tone", "0.4"}, {"output", "-3"}, {"cabinet", "1"}, {"bypass", "false"}} {
		source, _, _, _, err = mixerSource(source, "rig."+v.name, json.RawMessage(v.value))
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
	}
	for _, s := range []string{"drive = 0.7", "tone = 0.4", "output = -3dB", "cabinet = 1", "bypass = false"} {
		if !strings.Contains(string(source), s) {
			t.Fatalf("missing %q: %s", s, source)
		}
	}
}
