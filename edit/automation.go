package edit

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"m31labs.dev/cicada/kernel/seq"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/notation"
)

// SetAutomationPoint adds or edits one point of a parameter's automation lane
// (an `automate <path> { ... }` block). OldAt names the point being moved; it
// is empty for a new point. Shape is linear, step, smooth, exponential or
// curve and defaults to linear; Curve is the tension and only applies to curve.
type SetAutomationPoint struct {
	Entity EntityID `json:"entity"`
	OldAt  string   `json:"oldat,omitempty"`
	At     string   `json:"at"`
	Value  string   `json:"value"`
	Shape  string   `json:"shape,omitempty"`
	Curve  float64  `json:"curve,omitempty"`
}

func (SetAutomationPoint) Kind() string { return "setautomationpoint" }

// RemoveAutomationPoint removes the point at At (or at OldAt when given) and
// removes the lane when it was the lane's last point.
type RemoveAutomationPoint struct {
	Entity EntityID `json:"entity"`
	OldAt  string   `json:"oldat,omitempty"`
	At     string   `json:"at"`
	Shape  string   `json:"shape,omitempty"`
	Curve  float64  `json:"curve,omitempty"`
}

func (RemoveAutomationPoint) Kind() string { return "removeautomationpoint" }

func init() {
	Register("setautomationpoint", func() Intent { return &SetAutomationPoint{} })
	Register("removeautomationpoint", func() Intent { return &RemoveAutomationPoint{} })
	Handle("setautomationpoint", editAutomationPoint)
	Handle("removeautomationpoint", editAutomationPoint)
}

type automationPoint struct {
	oldAt, at, value, shape string
	curve                   float64
}

func editAutomationPoint(ctx *Context, intent Intent) error {
	var entity EntityID
	var point automationPoint
	remove := false
	switch in := intent.(type) {
	case *SetAutomationPoint:
		entity, point = in.Entity, automationPoint{in.OldAt, in.At, in.Value, in.Shape, in.Curve}
	case *RemoveAutomationPoint:
		entity, point, remove = in.Entity, automationPoint{in.OldAt, in.At, "", in.Shape, in.Curve}, true
	}
	if ctx.Options.CaptureGuard != nil {
		if err := ctx.Options.CaptureGuard(); err != nil {
			return err
		}
	}
	if _, err := ParseEntityID(string(entity)); err != nil {
		return err
	}
	if entity.Kind() != KindParam {
		return fmt.Errorf("automation points need a param entity, got %q", entity)
	}
	updated, err := automationPointSource(ctx, entity.Name(), point, remove)
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	action := "set"
	if remove {
		action = "removed"
	}
	ctx.SetLabel(fmt.Sprintf("Automation · %s point %s %s", entity.Name(), point.at, action))
	return nil
}

// automationPointSource edits one authored point; comments and all other
// declarations keep their bytes. It ports curvePointSource.
func automationPointSource(ctx *Context, path string, point automationPoint, remove bool) ([]byte, error) {
	source, file := ctx.Source, ctx.Options.Path
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return nil, err
	}
	if score == nil || hasErrors(ds) {
		return nil, fmt.Errorf("score must validate before editing automation")
	}

	for _, lane := range score.Automation {
		if lane.Path == path {
			file = lane.Position.File
			if file == "" {
				file = ctx.Options.Path
			}
			var supplied bool
			source, supplied = ctx.FileSource(file)
			if !supplied {
				return nil, fmt.Errorf("automation %s is outside the editable project sources", path)
			}
			break
		}
	}
	patch := func(span Span) ([]byte, error) {
		after, err := ReplaceSpan(source, file, span)
		if err != nil {
			return nil, err
		}
		return stageProjectSource(ctx, file, source, after), nil
	}

	plan, err := ctx.CurrentPlan()
	if err != nil || plan == nil || plan.ResolveParam == nil {
		return nil, fmt.Errorf("score parameters must validate before editing automation")
	}
	resolved, err := plan.ResolveParam(path)
	if err != nil {
		return nil, err
	}
	d := resolved.Descriptor
	if !d.Live || !d.Automatable || d.Curve == "toggle" || d.Unit == "enum" {
		return nil, fmt.Errorf("%s has no continuous numeric automation", path)
	}
	tick, err := notation.MusicalTicks(point.at, true)
	if err != nil || tick < 0 || tick > arrangementEnd(plan) || tick%240 != 0 {
		return nil, fmt.Errorf("choose a sixteenth-note position within the song")
	}
	old := tick
	if point.oldAt != "" {
		old, err = notation.MusicalTicks(point.oldAt, true)
		if err != nil {
			return nil, err
		}
	}
	shape := point.shape
	if shape == "" {
		shape = "linear"
	}
	if shape != "linear" && shape != "step" && shape != "smooth" && shape != "exponential" && shape != "curve" {
		return nil, fmt.Errorf("choose linear, step, smooth, exponential or curve")
	}
	if math.IsNaN(point.curve) || math.IsInf(point.curve, 0) || math.Abs(point.curve) > 8 || shape != "curve" && point.curve != 0 {
		return nil, fmt.Errorf("curve tension must be −8 to +8 and only applies to curve")
	}
	literal := ""
	if !remove {
		d.DisplayStep = 0
		raw, _ := json.Marshal(point.value)
		literal, err = CanonicalMixerLiteral(d, raw)
		if err != nil {
			return nil, err
		}
	}
	text := FormatPosition(tick) + " " + literal + " " + shape
	if shape == "curve" {
		text += " " + strconv.FormatFloat(point.curve, 'f', -1, 64)
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var lane *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) == "automate_decl" && w.Text(w.Field(n, "path")) == path {
			lane = n
			break
		}
	}
	nl := Newline(source)
	if lane == nil {
		if remove || point.oldAt != "" {
			return nil, fmt.Errorf("automation point no longer exists")
		}
		after := append(append([]byte(nil), source...), []byte(nl+"automate "+path+" {"+nl+"  "+text+nl+"}"+nl)...)
		return stageProjectSource(ctx, file, source, after), nil
	}
	var found *gts.Node
	count := 0
	insert := int(lane.EndByte()) - 1
	for i := 0; i < lane.NamedChildCount(); i++ {
		n := lane.NamedChild(i)
		if w.Type(n) != "automation_point" {
			continue
		}
		count++
		at, e := notation.MusicalTicks(w.Text(w.Field(n, "at")), true)
		if e != nil {
			return nil, e
		}
		if at == old {
			found = n
		}
		if at == tick && old != tick {
			return nil, fmt.Errorf("another automation point already occupies that position")
		}
		if at > tick && insert == int(lane.EndByte())-1 {
			insert = int(n.StartByte())
		}
	}
	if found != nil {
		if remove {
			if count == 1 {
				return patch(Span{int(lane.StartByte()), int(lane.EndByte()), "", file})
			}
			return patch(Span{int(found.StartByte()), int(found.EndByte()), "", file})
		}
		// Moving across neighbors is rejected by project validation; dragging in
		// Session clamps between them and keeps the lane's authored order.
		return patch(Span{int(found.StartByte()), int(found.EndByte()), text, file})
	}
	if remove || point.oldAt != "" {
		return nil, fmt.Errorf("automation point no longer exists")
	}
	return patch(Span{insert, insert, "  " + text + nl, file})
}

// arrangementEnd is the last tick of the arrangement: the end of the last
// placement, or the summed song blocks for a score without an arrange block.
func arrangementEnd(plan *Plan) int64 {
	var end int64
	if len(plan.Placements) > 0 {
		for _, place := range plan.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
		}
		return end
	}
	for _, entry := range plan.Song {
		end += int64(entry.Bars) * seq.TicksPerBar
	}
	return end
}
