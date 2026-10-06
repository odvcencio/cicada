package instrument

import (
	"fmt"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
	"testing"
)

func TestNeuralAmpLowerAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		expression string
		valid      bool
	}{
		{"neural_amp(saw(pitch), 2)", true},
		{"neural_amp(neural_amp(saw(pitch), 2), velocity)", true},
		{"neural_amp(saw(pitch), 1ms)", false},
		{"neural_amp(pitch, 2)", false},
		{"neural_amp(saw(pitch))", false},
	} {
		score, ds := notation.Parse([]byte(fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)))
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := Compile(score.Instruments[0])
		if !tc.valid {
			if len(ds) == 0 {
				t.Fatalf("accepted %s", tc.expression)
			}
			continue
		}
		if p == nil || len(ds) != 0 {
			t.Fatalf("compile: %+v", ds)
		}
		lowered, err := Lower(p, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, n := range lowered.Nodes[:lowered.Len] {
			found = found || n.Op == graph.NeuralAmp
		}
		if !found {
			t.Fatal("neural amp opcode missing")
		}
		if _, err := graph.NewVoice(lowered, 48000); err != nil {
			t.Fatal(err)
		}
	}
}
