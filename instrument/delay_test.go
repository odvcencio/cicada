package instrument

import (
	"fmt"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

func TestDelayDiagnostics(t *testing.T) {
	for _, tc := range []struct{ expression, code string }{
		{"delay(noise(), 10ms)", ""},
		{"comb(noise(), 1 / pitch, 0.99, 0.5)", ""},
		{"delay(noise(), 1 / 440Hz)", ""},
		{"delay(noise(), 440Hz)", "CICADA-UNIT"},
		{"comb(noise(), 10ms, 1ms, 0.5)", "CICADA-UNIT"},
		{"comb(noise(), 10ms, 0.5, 440Hz)", "CICADA-UNIT"},
		{"delay(noise(), 0ms)", "CICADA-PARAM"},
		{"delay(noise(), -1ms)", "CICADA-PARAM"},
		{"delay(noise(), 1s)", "CICADA-PARAM"},
		{"delay(noise(), 50ms * 2)", "CICADA-PARAM"},
		{"delay(noise(), 1 / 1Hz)", "CICADA-PARAM"},
		{"comb(noise(), 10ms, 1, 0.5)", "CICADA-PARAM"},
		{"comb(noise(), 10ms, 0.99, -0.1)", "CICADA-PARAM"},
		{"delay(delay(delay(noise(), 1ms), 2ms), 3ms)", "CICADA-LIMIT"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			score, ds := notation.Parse([]byte(fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes { a3 } scene s { t=p } song { s }", tc.expression)))
			if score == nil || len(ds) != 0 {
				t.Fatalf("parse: %+v", ds)
			}
			p, ds := Compile(score.Instruments[0])
			if tc.code == "" {
				if p == nil || len(ds) != 0 {
					t.Fatalf("compile: %+v", ds)
				}
				lowered, err := Lower(p, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := graph.NewVoice(lowered, 48_000); err != nil {
					t.Fatal(err)
				}
				return
			}
			if len(ds) == 0 || ds[0].Code != tc.code || ds[0].Position.Line == 0 {
				t.Fatalf("want %s: %+v", tc.code, ds)
			}
		})
	}
}
