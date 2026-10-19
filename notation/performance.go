package notation

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

func parsePerformedExpression(w *loweringWalker, point *gts.Node) (seq.PerformedExpression, error) {
	at, err := strconv.ParseInt(strings.TrimSuffix(w.Text(w.Field(point, "at")), "ticks"), 10, 64)
	if err != nil || at < 0 || at > 1<<40 {
		return seq.PerformedExpression{}, fmt.Errorf("gesture position must be a valid tick offset from its note")
	}
	value := seq.PerformedExpression{Tick: at, Params: seq.Expression{Set: true, Timbre: .5}}
	for _, field := range []struct {
		name string
		out  *float32
	}{
		{"bend", &value.Params.PitchCents}, {"pressure", &value.Params.Pressure},
		{"timbre", &value.Params.Timbre}, {"vibrato", &value.Params.VibratoDepthCents},
	} {
		node := w.Field(point, field.name)
		if node == nil {
			continue
		}
		*field.out, err = ExpressionValue(field.name, w.Text(node))
		if err != nil {
			return value, err
		}
	}
	if value.Params.VibratoDepthCents != 0 {
		value.Params.VibratoRateHz = 5
	}
	if node := w.Field(point, "rate"); node != nil {
		text := w.Text(node)
		rate, err := strconv.ParseFloat(strings.TrimSuffix(text, "Hz"), 64)
		if err != nil || !strings.HasSuffix(text, "Hz") || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 100 {
			return value, fmt.Errorf("gesture vibrato rate must be 0 to 100Hz")
		}
		value.Params.VibratoRateHz = float32(rate)
	}
	return value, nil
}

// PerformedNoteSource is shared by project exports and typed source edits.
// Gesture positions are relative to the note, with canonical float32 spelling.
func PerformedNoteSource(note seq.PerformedNote, groups ...seq.ControlGroup) string {
	var out strings.Builder
	fmt.Fprintf(&out, "  note %d at=%dticks length=%dticks velocity=%d", note.Note, note.Tick, note.EndTick-note.Tick, note.Velocity)
	if note.Group > 0 && int(note.Group) <= len(groups) {
		fmt.Fprintf(&out, " group=%s", groups[note.Group-1].Name)
	} else if note.Group > 0 {
		// Missing context must fail as an unresolved reference on re-parse,
		// never silently export an unbound note with a different sound.
		fmt.Fprintf(&out, " group=missing-shared-lane-%d", note.Group)
	}
	if len(note.Expressions) == 0 {
		out.WriteString(";\n")
		return out.String()
	}
	out.WriteString(" {\n")
	number := func(value float32) string { return strconv.FormatFloat(float64(value), 'f', -1, 32) }
	for _, point := range note.Expressions {
		p := point.Params
		fmt.Fprintf(&out, "    gesture at=%dticks bend=%sct pressure=%s timbre=%s", point.Tick, number(p.PitchCents), number(p.Pressure), number(p.Timbre))
		if p.VibratoDepthCents != 0 {
			fmt.Fprintf(&out, " vibrato=%sct", number(p.VibratoDepthCents))
		}
		if p.VibratoRateHz != 0 || p.VibratoDepthCents != 0 {
			fmt.Fprintf(&out, " rate=%sHz", number(p.VibratoRateHz))
		}
		out.WriteString(";\n")
	}
	out.WriteString("  }\n")
	return out.String()
}

func PerformedGroupSource(group seq.ControlGroup) string {
	var out strings.Builder
	fmt.Fprintf(&out, "  gesture-group %s {\n", group.Name)
	number := func(value float32) string { return strconv.FormatFloat(float64(value), 'f', -1, 32) }
	for _, point := range group.Points {
		v := point.Value
		fmt.Fprintf(&out, "    gesture at=%dticks bend=%sct pressure=%s timbre=%s;\n", point.Tick, number(v.PitchCents), number(v.Pressure), number(v.Timbre))
	}
	out.WriteString("  }\n")
	return out.String()
}

func parsePerformedGroup(w *loweringWalker, node *gts.Node) (seq.ControlGroup, error) {
	group := seq.ControlGroup{Name: w.Text(w.Field(node, "name"))}
	for i := 0; i < node.NamedChildCount(); i++ {
		point := node.NamedChild(i)
		if w.Type(point) != "performed_expression" {
			continue
		}
		value, err := parsePerformedExpression(w, point)
		if err != nil {
			return group, err
		}
		if w.Field(point, "vibrato") != nil || w.Field(point, "rate") != nil {
			return group, fmt.Errorf("shared gestures carry bend, pressure and timbre; put vibrato on each note")
		}
		group.Points = append(group.Points, seq.GroupPoint{Tick: value.Tick, Value: expression.Controls{PitchCents: value.Params.PitchCents, Pressure: value.Params.Pressure, Timbre: value.Params.Timbre}})
	}
	return group, nil
}
