package instrument

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

func TestPMTypesAndLimits(t *testing.T) {
	for _, tc := range []struct{ expression, code string }{
		{"pm(pitch, sine(pitch * 3.5), 4 * env(gate, 260ms))", ""},
		{"pm(pitch, sine(pitch), -4)", ""},
		{"pm(1ms, sine(pitch), 4)", "CICADA-UNIT"},
		{"pm(pitch, 1, 4)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch), 1Hz)", "CICADA-UNIT"},
		{"pm(pitch, sine(pitch))", "CICADA-PARAM"},
		{"pm(pitch, sine(pitch), 4, 1)", "CICADA-PARAM"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			score, ds := notation.Parse([]byte(fmt.Sprintf("instrument i { voice mono { out = %s } } track t i {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)))
			if score == nil || len(ds) != 0 {
				t.Fatalf("parse: %+v", ds)
			}
			p, ds := Compile(score.Instruments[0])
			if tc.code != "" {
				if len(ds) == 0 || ds[0].Code != tc.code || ds[0].Position.Line == 0 {
					t.Fatalf("want %s: %+v", tc.code, ds)
				}
				return
			}
			if p == nil || len(ds) != 0 {
				t.Fatalf("compile: %+v", ds)
			}
			lowered, err := Lower(p, nil)
			if err != nil || lowered.Nodes[lowered.Output].Op != graph.PM || p.StatefulNodes < 2 || p.DelaySamples != 0 {
				t.Fatalf("PM lowering/state: %+v %v", p, err)
			}
		})
	}
	for _, count := range []int{121, 122} {
		var lets strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&lets, "let n%d = 1; ", i)
		}
		score, ds := notation.Parse([]byte("instrument i { voice mono { " + lets.String() + "out = pm(pitch, sine(pitch), 1) } } track t i {} pattern p notes { a3 } scene s { t=p } song { s }"))
		if score == nil || len(ds) != 0 {
			t.Fatalf("parse: %+v", ds)
		}
		p, ds := Compile(score.Instruments[0])
		if count == 121 && (p == nil || len(p.Nodes) != 128) || count == 122 && (len(ds) == 0 || ds[0].Code != "CICADA-LIMIT") {
			t.Fatalf("128-node boundary: %v %+v", p, ds)
		}
	}
}
