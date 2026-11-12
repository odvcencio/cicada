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

func TestSharedLaneRenameStaysInsidePerformance(t *testing.T) {
	source := []byte(`cicada 2
instrument first { param touch=0.5 voice poly { out=sine(pitch)*touch } }
track keys first {}
pattern one performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=103 group=touch;
 gesture-group touch { gesture at=0ticks bend=0ct pressure=0 timbre=0.5; }
}
pattern two performance length=3840ticks {
 gesture-group touch { gesture at=0ticks bend=0ct pressure=0 timbre=0.5; }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
scene main { keys=one }
song { main }
`)
	reference := bytes.Index(source, []byte("group=touch")) + len("group=")
	edit := rename("file:///score.cicada", source, utf16Position(source, reference), "breath")
	encoded, _ := json.Marshal(edit)
	if bytes.Count(encoded, []byte(`"newText":"breath"`)) != 2 || bytes.Contains(encoded, []byte(`"line":1,`)) {
		t.Fatalf("shared rename crossed its scope: %s", encoded)
	}
	location := definition("file:///score.cicada", source, utf16Position(source, reference))
	encoded, _ = json.Marshal(location)
	if !bytes.Contains(encoded, []byte(`"line":5`)) {
		t.Fatalf("forward shared definition resolved outside the pattern: %s", encoded)
	}
}

// TestRenameFollowsEveryTrackReference renames a track that MIDI, live
// layers, an envelope source and a quoted compressor sidechain name, which
// the symbols query does not tag; the shared finder supplies them.
func TestRenameFollowsEveryTrackReference(t *testing.T) {
	source := []byte("cicada 2\ntempo 120\nfx glue comp { sidechain = \"bass\" }\nbus music { insert = glue }\ntrack bass acid {}\ntrack keys piano {}\nenvelope follow { source = bass attack = 10ms release = 200ms }\nmod follow -> keys.level depth = 3dB\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main }\nmidi {\n  port pads\n  pads ch 1 -> bass\n  pads note c2 -> stop-track bass\n}\nlive {\n  macro intensity = 0.3\n  layers intensity { bass >= 0.25 }\n}\n")
	uri := "file:///score.cicada"
	at := bytes.Index(source, []byte("bass acid"))
	encoded, _ := json.Marshal(rename(uri, source, utf16Position(source, at), "low"))
	// The declaration, the scene binding, the sidechain, the envelope source,
	// two MIDI mappings and the live layer.
	if got := bytes.Count(encoded, []byte(`"newText":"low"`)); got != 7 {
		t.Fatalf("track rename made %d edits, want 7: %s", got, encoded)
	}
}
