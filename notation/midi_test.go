package notation

import (
	"strings"
	"testing"
)

const midiScore = `cicada 2
track bass acid {}
track lead acid {}
midi {
  port keys
  port pads
  keys ch 1 -> lead
  keys cc 74 -> bass.cutoff 200Hz..4kHz takeover = pickup
  pads note c1 -> launch verse
  pads ch 2 note 60 -> stop
  pads note 61 -> stop-track bass
  pads note 62 -> launch-slot bass phrase
}
pattern phrase { 1 }
scene verse { bass = phrase lead = phrase }
song { verse }
`

func TestPortableMIDIParseAndValidate(t *testing.T) {
	s, diagnostics := Parse([]byte(midiScore))
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if s.MIDI == nil || len(s.MIDI.Ports) != 2 || len(s.MIDI.Mappings) != 6 {
		t.Fatalf("wrong MIDI mappings: %+v", s.MIDI)
	}
	if got := s.MIDI.Mappings[2]; got.Number != 24 || got.Target != "launch verse" {
		t.Fatalf("incorrect pitch or action: %+v", got)
	}
	if got := s.MIDI.Mappings[1].Takeover; got != "pickup" {
		t.Fatalf("lost pickup mode: %q", got)
	}
	if s.MIDI.Mappings[4].Target != "stop-track bass" || s.MIDI.Mappings[5].Target != "launch-slot bass phrase" {
		t.Fatalf("lost track or slot action: %+v", s.MIDI.Mappings)
	}
	qualified := strings.Replace(midiScore, "launch-slot bass phrase", "launch-slot bass library.phrase", 1)
	if score, ds := Parse([]byte(qualified)); len(ds) != 0 || score.MIDI.Mappings[5].Target != "launch-slot bass library.phrase" {
		t.Fatalf("qualified slot target: %+v %v", score, ds)
	}
	for _, bad := range []string{
		strings.Replace(midiScore, "keys cc 74", "other cc 74", 1),
		strings.Replace(midiScore, "keys cc 74", "keys cc 128", 1),
		strings.Replace(midiScore, "keys ch 1", "keys ch 17", 1),
		strings.Replace(midiScore, "bass.cutoff", "bass.missing", 1),
		strings.Replace(midiScore, "200Hz..4kHz", "4kHz..200Hz", 1),
		strings.Replace(midiScore, "keys ch 1", "keys ch 0", 1),
		strings.Replace(midiScore, "takeover = pickup", "takeover = scale", 1),
		strings.Replace(midiScore, "stop-track bass", "stop-track missing", 1),
		strings.Replace(midiScore, "keys cc 74", "keys cc 99999999999999999999999999", 1),
	} {
		_, ds := Parse([]byte(bad))
		if len(ds) == 0 {
			t.Fatalf("invalid MIDI score accepted: %s", bad)
		}
	}
	document, err := ParseDocument([]byte(midiScore))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := Parse(formatted); len(ds) != 0 {
		t.Fatalf("format broke MIDI score: %v\n%s", ds, formatted)
	}
}
