package lsp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDefinitionAndRenamePattern(t *testing.T) {
	source := []byte("track bass acid {}\npattern hook acid steps=1 { 1 }\nscene main { bass=hook }\nsong { main }\n")
	uri := "file:///score.cicada"
	reference := bytes.Index(source, []byte("bass=hook")) + len("bass=")
	location := definition(uri, source, utf16Position(source, reference))
	encoded, _ := json.Marshal(location)
	if !bytes.Contains(encoded, []byte(`"line":1`)) {
		t.Fatalf("pattern definition: %s", encoded)
	}
	edit := rename(uri, source, utf16Position(source, reference), "new-hook")
	encoded, _ = json.Marshal(edit)
	if bytes.Count(encoded, []byte(`"newText":"new-hook"`)) != 2 {
		t.Fatalf("pattern rename should edit definition and reference: %s", encoded)
	}
	if rename(uri, source, utf16Position(source, reference), "8bad") != nil {
		t.Fatal("invalid name accepted")
	}
	trackDefinition := bytes.Index(source, []byte("bass acid"))
	trackEdit := rename(uri, source, utf16Position(source, trackDefinition), "low")
	encoded, _ = json.Marshal(trackEdit)
	if bytes.Count(encoded, []byte(`"newText":"low"`)) != 2 {
		t.Fatalf("track rename did not follow scene reference: %s", encoded)
	}
}

func TestLocalParameterRenameStaysInsideInstrument(t *testing.T) {
	source := []byte("instrument first { param cutoff = 500Hz voice mono { out = saw(cutoff) } }\ninstrument second { param cutoff = 600Hz voice mono { out = saw(cutoff) } }\ntrack lead first {}\npattern p notes steps=1 { c }\nscene main { lead=p }\nsong { main }\n")
	first := bytes.Index(source, []byte("saw(cutoff)")) + len("saw(")
	edit := rename("file:///score.cicada", source, utf16Position(source, first), "edge")
	encoded, _ := json.Marshal(edit)
	if bytes.Count(encoded, []byte(`"newText":"edge"`)) != 2 {
		t.Fatalf("local rename leaked into another instrument: %s", encoded)
	}
	if strings.Contains(string(encoded), `"line":1`) {
		t.Fatalf("second instrument was edited: %s", encoded)
	}
	location := definition("file:///score.cicada", source, utf16Position(source, first))
	encoded, _ = json.Marshal(location)
	if !bytes.Contains(encoded, []byte(`"line":0`)) {
		t.Fatalf("parameter definition: %s", encoded)
	}
}
