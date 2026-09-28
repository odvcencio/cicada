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

func TestNamedMixerPathNavigationAndCompletion(t *testing.T) {
	source := []byte("fx room delay {}\nfx warmth drive {}\nbus music { mute = off }\ntrack bass acid { insert = warmth send room = 0.2 pre out = music }\npattern riff acid steps=1 { 1 }\nscene main { bass = riff room.feedback = 0.3 bass.send.delay = 0.4 }\nsong { main }\n")
	uri := "file:///named-mixer.cicada"
	pathOffset := bytes.Index(source, []byte("room.feedback")) + 2
	hoverValue, _ := json.Marshal(hover(source, utf16Position(source, pathOffset)))
	if !bytes.Contains(hoverValue, []byte("effect `room`")) {
		t.Fatalf("named effect hover did not resolve: %s", hoverValue)
	}
	location, _ := json.Marshal(definition(uri, source, utf16Position(source, pathOffset)))
	if !bytes.Contains(location, []byte(`"line":0`)) {
		t.Fatalf("named effect definition did not select its declaration: %s", location)
	}
	busOffset := bytes.Index(source, []byte("out = music")) + len("out = ")
	busLocation, _ := json.Marshal(definition(uri, source, utf16Position(source, busOffset)))
	if !bytes.Contains(busLocation, []byte(`"line":2`)) {
		t.Fatalf("bus reference did not resolve to its declaration: %s", busLocation)
	}
	completionSource := []byte("fx room delay {}\ntrack bass acid {}\nbus sfx {}\nscene main { bass.send.\n}\n")
	items := parameterPathCompletion(completionSource, utf16Position(completionSource, bytes.Index(completionSource, []byte("bass.send."))+len("bass.send.")))
	encoded, _ := json.Marshal(items)
	if !bytes.Contains(encoded, []byte(`"label":"delay"`)) {
		t.Fatalf("named send path completion missing delay: %s", encoded)
	}
	trackCompletionSource := []byte("fx room delay {}\nfx warmth drive {}\nfx glue comp {}\ntrack bass acid {\n  insert = warmth\n  send room = 0.2\n  out = music\n}\nbus music {\n  insert = glue\n}\n")
	trackInsert := parameterPathCompletion(trackCompletionSource, utf16Position(trackCompletionSource, bytes.Index(trackCompletionSource, []byte("warmth\n"))))
	trackInsertJSON, _ := json.Marshal(trackInsert)
	if !bytes.Contains(trackInsertJSON, []byte(`"label":"warmth"`)) || bytes.Contains(trackInsertJSON, []byte(`"label":"glue"`)) {
		t.Fatalf("track insert completion offered a non-drive: %s", trackInsertJSON)
	}
	sendCompletion := parameterPathCompletion(trackCompletionSource, utf16Position(trackCompletionSource, bytes.Index(trackCompletionSource, []byte("room ="))))
	sendCompletionJSON, _ := json.Marshal(sendCompletion)
	if !bytes.Contains(sendCompletionJSON, []byte(`"label":"room"`)) {
		t.Fatalf("track send completion omitted declared delay: %s", sendCompletionJSON)
	}
	busInsert := parameterPathCompletion(trackCompletionSource, utf16Position(trackCompletionSource, bytes.LastIndex(trackCompletionSource, []byte("glue"))))
	busInsertJSON, _ := json.Marshal(busInsert)
	if !bytes.Contains(busInsertJSON, []byte(`"label":"glue"`)) || bytes.Contains(busInsertJSON, []byte(`"label":"warmth"`)) {
		t.Fatalf("music bus insert completion offered a non-compressor: %s", busInsertJSON)
	}
	declarationOffset := bytes.Index(source, []byte("fx room")) + len("fx ")
	edit := rename(uri, source, utf16Position(source, declarationOffset), "chamber")
	encoded, _ = json.Marshal(edit)
	if bytes.Count(encoded, []byte(`"newText":"chamber"`)) < 3 {
		t.Fatalf("effect rename did not update declaration, send target, and path owner: %s", encoded)
	}
	busDeclaration := bytes.Index(source, []byte("bus music")) + len("bus ")
	busEdit := rename(uri, source, utf16Position(source, busDeclaration), "sfx")
	encoded, _ = json.Marshal(busEdit)
	if bytes.Count(encoded, []byte(`"newText":"sfx"`)) < 2 {
		t.Fatalf("bus rename did not update declaration and output: %s", encoded)
	}
}
