package edit

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestRecordedExpressionRefusesForeignTokens(t *testing.T) {
	source := []byte("track lead acid {}\npattern melody acid steps=4 { . . . .\n bend: 0ct 0ct 0ct 0ct\n}\nscene main { lead=melody }\nsong { main }\n")
	for _, foreign := range []string{"pattern", "unused step", "expression"} {
		t.Run(foreign, func(t *testing.T) {
			score, ds := notation.ParseFiles([]notation.SourceFile{{Path: "main.cicada", Source: source}}, 2)
			if score == nil || hasErrors(ds) || len(score.Patterns) != 1 {
				t.Fatalf("invalid fixture: %v", ds)
			}
			authored := &score.Patterns[0]
			switch foreign {
			case "pattern":
				authored.Position.File = "part.cicada"
			case "unused step":
				authored.Steps[2].Position.File = "part.cicada"
			case "expression":
				authored.Expression[0].Values[0].Position.File = "part.cicada"
			}
			before := bytes.Clone(source)
			result, err := recordedExpressionSource(source, "main.cicada", score, &Pattern{ID: "melody", Steps: 4}, []TakeNote{{Note: 60, EndTick: 120, Expressions: []TakeExpression{{PitchCents: 50, Timbre: .5}}}})
			if err == nil || !strings.Contains(err.Error(), "another source file") || result != nil || !bytes.Equal(source, before) {
				t.Fatalf("foreign %s must refuse unchanged: candidate=%v error=%v", foreign, result != nil, err)
			}
		})
	}
}
