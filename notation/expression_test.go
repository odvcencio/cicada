package notation

import (
	"bytes"
	"strings"
	"testing"
)

const expressionSource = `track lead acid {}
pattern take {
  c4 - - .
  bend: +50ct . -1200ct 0ct
  vibrato: . 4ct 12ct 0ct
  pressure: 0 . 0.7 0
  timbre: . 0.8 . 0.5
}
scene main { lead = take }
song { main }
`

func TestExpressionRowsParseAndFormatLosslessly(t *testing.T) {
	score, diagnostics := Parse([]byte(expressionSource))
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	pattern := score.Patterns[0]
	if len(pattern.Steps) != 4 || len(pattern.Expression) != 4 {
		t.Fatalf("wrong expression shape: %+v", pattern)
	}
	if cell := pattern.Expression[0].Values[2]; cell.Text != "-1200ct" || cell.Position != (Position{Line: 4, Column: 17}) {
		t.Fatalf("expression source position: %+v", cell)
	}
	document, err := ParseDocument([]byte(expressionSource))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range pattern.Expression {
		var cells []string
		for _, cell := range row.Values {
			cells = append(cells, cell.Text)
		}
		if !strings.Contains(string(formatted), row.Name+": "+strings.Join(cells, " ")) {
			t.Fatalf("formatter changed expression cells: %s", formatted)
		}
	}
	document, err = ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Format(document)
	if err != nil || !bytes.Equal(formatted, second) {
		t.Fatalf("format is not idempotent: %s (%v)", second, err)
	}
	if _, diagnostics := Parse(formatted); len(diagnostics) != 0 {
		t.Fatalf("formatted parse: %+v", diagnostics)
	}
}

func TestExpressionRowsValidateUnitsRangeAndLength(t *testing.T) {
	for _, test := range []struct {
		name string
		row  string
		code string
	}{
		{"bend maximum", "bend: 9601ct 0ct", "CICADA-PARAM"},
		{"bend minimum", "bend: -9601ct 0ct", "CICADA-PARAM"},
		{"bend unit", "bend: 50 0ct", "CICADA-PARAM"},
		{"negative depth", "vibrato: -1ct 0ct", "CICADA-PARAM"},
		{"depth maximum", "vibrato: 9601ct 0ct", "CICADA-PARAM"},
		{"pressure range", "pressure: 1.1 0", "CICADA-PARAM"},
		{"timbre unit", "timbre: 0.5ct 0", "CICADA-PARAM"},
		{"row length", "bend: 0ct", "CICADA-PARAM"},
		{"duplicate", "bend: 0ct . bend: 1ct .", "CICADA-DUPLICATE"},
		{"unknown", "modulation: 0 .", "CICADA-PARAM"},
		{"unknown holds", "modulation: . .", "CICADA-PARAM"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "track lead acid {} pattern take { c4 - " + test.row + " } scene main { lead=take } song { main }"
			_, diagnostics := Parse([]byte(source))
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics: %+v", diagnostics)
			}
		})
	}
	for _, row := range []string{"bend: -9600ct +9600ct", "vibrato: 0ct 9600ct", "pressure: . 1", "timbre : . 0"} {
		source := "track lead acid {} pattern take { c4 - " + row + " } scene main { lead=take } song { main }"
		if _, diagnostics := Parse([]byte(source)); len(diagnostics) != 0 {
			t.Fatalf("valid boundary %q: %+v", row, diagnostics)
		}
	}
}
