package project

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func unsupportedSourceExpression(score *notation.Score, kind string) string {
	for _, sampler := range score.Samplers {
		if sampler.Name == kind {
			return "sampler"
		}
	}
	if notationKeysBuiltin(score, kind) {
		return "modeled keyboard"
	}
	if kind != "piano" {
		return ""
	}
	for _, instrument := range score.Instruments {
		if instrument.Name == kind {
			return ""
		}
	}
	for _, kit := range score.Kits {
		if kit.Name == kind {
			return ""
		}
	}
	return "modeled piano"
}

func compileExpression(source notation.Pattern, pattern *seq.Pattern) error {
	if len(source.Expression) == 0 {
		return nil
	}
	if source.Kind == "drums" {
		return fmt.Errorf("drum patterns do not support expression rows")
	}
	pattern.Expression = new([64]seq.Expression)
	for i := 0; i < int(pattern.Len); i++ {
		pattern.Expression[i] = seq.Expression{Set: true, Timbre: 0.5, VibratoRateHz: 5}
	}
	seen := map[string]bool{}
	for _, row := range source.Expression {
		if seen[row.Name] || len(row.Values) != int(pattern.Len) {
			return fmt.Errorf("expression row %s is duplicated or has the wrong length", row.Name)
		}
		seen[row.Name] = true
		value := float32(0)
		if row.Name == "timbre" {
			value = 0.5
		}
		for i, cell := range row.Values {
			if cell.Text != "." {
				var err error
				value, err = notation.ExpressionValue(row.Name, cell.Text)
				if err != nil {
					return &patternCompileError{position: cell.Position, err: err}
				}
			}
			expression := &pattern.Expression[i]
			switch row.Name {
			case "bend":
				expression.PitchCents = value
			case "vibrato":
				expression.VibratoDepthCents = value
			case "pressure":
				expression.Pressure = value
			case "timbre":
				expression.Timbre = value
			default:
				return fmt.Errorf("unknown expression row %s", row.Name)
			}
		}
	}
	return nil
}

func projectExpression(pattern seq.Pattern) []NoteExpression {
	if pattern.Len == 0 || !pattern.ExpressionAt(0).Set {
		return nil
	}
	values := make([]NoteExpression, pattern.Len)
	for i := range values {
		source := pattern.ExpressionAt(i)
		values[i] = NoteExpression{
			PitchCents: source.PitchCents, Pressure: source.Pressure, Timbre: source.Timbre,
			VibratoDepthCents: source.VibratoDepthCents,
		}
	}
	return values
}

func kernelExpression(source NoteExpression) seq.Expression {
	return seq.Expression{
		PitchCents: source.PitchCents, Pressure: source.Pressure, Timbre: source.Timbre,
		VibratoRateHz: 5, VibratoDepthCents: source.VibratoDepthCents, Set: true,
	}
}

func validatePatternExpression(pattern Pattern) error {
	if len(pattern.Expression) == 0 {
		return nil
	}
	if pattern.Kind == "drums" || len(pattern.Expression) != int(pattern.Steps) {
		return fmt.Errorf("expression needs one value per melodic step")
	}
	for i, expression := range pattern.Expression {
		for _, value := range []struct {
			name    string
			number  float32
			minimum float32
			maximum float32
		}{
			{"pitch cents", expression.PitchCents, -9600, 9600},
			{"pressure", expression.Pressure, 0, 1},
			{"timbre", expression.Timbre, 0, 1},
			{"vibrato depth", expression.VibratoDepthCents, 0, 9600},
		} {
			if math.IsNaN(float64(value.number)) || math.IsInf(float64(value.number), 0) || value.number < value.minimum || value.number > value.maximum {
				return fmt.Errorf("step %d expression %s must be %g to %g", i+1, value.name, value.minimum, value.maximum)
			}
		}
	}
	return nil
}

func writeExpressionSource(out *strings.Builder, expression []NoteExpression) {
	if len(expression) == 0 {
		return
	}
	for _, row := range []struct {
		name  string
		unit  string
		value func(NoteExpression) float32
	}{
		{"bend", "ct", func(value NoteExpression) float32 { return value.PitchCents }},
		{"vibrato", "ct", func(value NoteExpression) float32 { return value.VibratoDepthCents }},
		{"pressure", "", func(value NoteExpression) float32 { return value.Pressure }},
		{"timbre", "", func(value NoteExpression) float32 { return value.Timbre }},
	} {
		out.WriteString("  " + row.name + ":")
		previous := float32(0)
		for i, step := range expression {
			value := row.value(step)
			text := "."
			if i == 0 || value != previous {
				text = strconv.FormatFloat(float64(value), 'f', -1, 32) + row.unit
			}
			out.WriteString(" " + text)
			previous = value
		}
		out.WriteByte('\n')
	}
}
