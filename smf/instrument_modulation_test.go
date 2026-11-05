package smf

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestInstrumentModulationDoesNotChangeMIDI(t *testing.T) {
	data, err := os.ReadFile("../examples/detail/instrument-modulation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	reference := strings.Replace(string(data), "1200Hz * (1 + 0.4 * mod(wander))", "1200Hz", 1)
	if reference == string(data) {
		t.Fatal("example no longer contains the reference modulation")
	}
	midi := func(source string) []byte {
		score, ds := notation.Parse([]byte(source))
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		p, ds := project.FromScore(score)
		if p == nil {
			t.Fatal(ds)
		}
		file, err := FromProject(p, 0)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := Encode(file, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	if !bytes.Equal(midi(string(data)), midi(reference)) {
		t.Fatal("modulation changed the MIDI export")
	}
}
