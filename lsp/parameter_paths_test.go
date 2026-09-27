package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParameterPathHoverAndDefinition(t *testing.T) {
	source := []byte("track bass acid { cutoff = 700Hz }\nfx delay { feedback = 0.2 }\npattern riff acid steps=1 { 1 }\nscene drop { bass = riff bass.cutoff = 900Hz delay.feedback = 0.3 }\nsong { drop }\n")
	uri := "file:///score.cicada"
	pathOffset := bytes.Index(source, []byte("bass.cutoff")) + 3
	hoverValue, _ := json.Marshal(hover(source, utf16Position(source, pathOffset)))
	for _, want := range []string{"bass.cutoff", "track `bass`", "Hz", "20..8000Hz", "Default: 600Hz", "Live: true", "Display step: 1Hz"} {
		if !bytes.Contains(hoverValue, []byte(want)) {
			t.Fatalf("parameter hover missing %q: %s", want, hoverValue)
		}
	}
	location, _ := json.Marshal(definition(uri, source, utf16Position(source, pathOffset)))
	if !bytes.Contains(location, []byte(`"line":0`)) || !bytes.Contains(location, []byte(`"character":6`)) {
		t.Fatalf("path definition did not select the track owner: %s", location)
	}
	effectOffset := bytes.Index(source, []byte("delay.feedback")) + 3
	effectHover, _ := json.Marshal(hover(source, utf16Position(source, effectOffset)))
	if !bytes.Contains(effectHover, []byte("effect `delay`")) {
		t.Fatalf("effect path hover did not resolve its owner: %s", effectHover)
	}
}

func TestParameterPathCompletionAndTrackRename(t *testing.T) {
	partial := []byte("track bass acid {}\nscene main { bass.\n}\n")
	items := parameterPathCompletion(partial, utf16Position(partial, bytes.Index(partial, []byte("bass."))+len("bass.")))
	encoded, _ := json.Marshal(items)
	for _, want := range []string{"cutoff", "send.delay", "level"} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Fatalf("track completion missing %q: %s", want, encoded)
		}
	}

	source := []byte("track bass acid {}\npattern riff acid steps=1 { 1 }\nscene main { bass = riff bass.cutoff = 900Hz }\nsong { main }\n")
	uri := "file:///score.cicada"
	trackOffset := bytes.Index(source, []byte("bass acid"))
	edit := rename(uri, source, utf16Position(source, trackOffset), "low")
	encoded, _ = json.Marshal(edit)
	if bytes.Count(encoded, []byte(`"newText":"low"`)) != 3 {
		t.Fatalf("track rename did not update binding and path owner: %s", encoded)
	}
	pathOffset := bytes.Index(source, []byte("bass.cutoff")) + 2
	fromPath := rename(uri, source, utf16Position(source, pathOffset), "low")
	encoded, _ = json.Marshal(fromPath)
	if !strings.Contains(string(encoded), `"newText":"low"`) {
		t.Fatalf("rename on a path owner did not resolve to its track: %s", encoded)
	}
}
