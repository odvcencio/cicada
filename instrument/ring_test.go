package instrument

import (
	"fmt"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

// Audio times audio is ring modulation; gates still cannot be multiplied.
func TestAudioTimesAudioTypesAndLowers(t *testing.T) {
	for _, tc := range []struct {
		expression string
		ok         bool
	}{
		{"noise() * square(pitch) * env(gate, 100ms)", true},
		{"sine(pitch) * sine(pitch * 1.5)", true},
		{"(sine(pitch) * sine(pitch)) * (sine(pitch) * sine(pitch))", true},
		{"sine(pitch) * gate", false},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			score, ds := notation.Parse([]byte(fmt.Sprintf("instrument i { voice mono { out = %s } } track t i {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)))
			if score == nil || len(ds) != 0 {
				t.Fatalf("parse: %+v", ds)
			}
			p, ds := Compile(score.Instruments[0])
			if !tc.ok {
				if len(ds) == 0 || ds[0].Code != "CICADA-UNIT" {
					t.Fatalf("want CICADA-UNIT: %+v", ds)
				}
				return
			}
			if p == nil || len(ds) != 0 {
				t.Fatalf("compile: %+v", ds)
			}
			lowered, err := Lower(p, nil)
			if err != nil {
				t.Fatal(err)
			}
			if p.Nodes[p.Output].Type != Audio {
				t.Fatalf("output type %s, want audio", p.Nodes[p.Output].Type)
			}
			found := false
			for _, node := range lowered.Nodes {
				found = found || node.Op == graph.Multiply
			}
			if !found {
				t.Fatal("no multiply node")
			}
		})
	}
}
