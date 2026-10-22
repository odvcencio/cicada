package edit

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

// SetPerformedNote edits an authored gate. Index equal to the note count appends.
// Pitch is the stored MIDI pitch, before an authored pattern transpose.
type SetPerformedNote struct {
	Entity   EntityID `json:"entity"`
	Index    int      `json:"index"`
	Tick     int64    `json:"tick"`
	EndTick  int64    `json:"end_tick"`
	Pitch    int      `json:"pitch"`
	Velocity int      `json:"velocity"`
	Remove   bool     `json:"remove,omitempty"`
}

func (SetPerformedNote) Kind() string { return "setperformednote" }

type ResizePerformance struct {
	Entity      EntityID `json:"entity"`
	LengthTicks int64    `json:"length_ticks"`
}

func (ResizePerformance) Kind() string { return "resizeperformance" }

// SetPerformedExpression edits one note-owned controller sample. Index equal
// to the sample count appends; edits preserve every other note and gesture.
type SetPerformedExpression struct {
	Entity            EntityID `json:"entity"`
	NoteIndex         int      `json:"note_index"`
	Index             int      `json:"index"`
	Tick              int64    `json:"tick"`
	PitchCents        float64  `json:"pitch_cents"`
	Pressure          float64  `json:"pressure"`
	Timbre            float64  `json:"timbre"`
	VibratoDepthCents float64  `json:"vibrato_depth_cents"`
	VibratoRateHz     float64  `json:"vibrato_rate_hz"`
	Remove            bool     `json:"remove,omitempty"`
}

func (SetPerformedExpression) Kind() string { return "setperformedexpression" }

type SetPerformanceGroup struct {
	Entity EntityID `json:"entity"`
	Index  int      `json:"index"`
	Name   string   `json:"name"`
	Remove bool     `json:"remove,omitempty"`
}

func (SetPerformanceGroup) Kind() string { return "setperformancegroup" }

type BindPerformedGroup struct {
	Entity    EntityID `json:"entity"`
	NoteIndex int      `json:"note_index"`
	Group     int      `json:"group"`
}

func (BindPerformedGroup) Kind() string { return "bindperformedgroup" }

type SetSharedExpression struct {
	Entity     EntityID `json:"entity"`
	GroupIndex int      `json:"group_index"`
	Index      int      `json:"index"`
	Tick       int64    `json:"tick"`
	PitchCents float64  `json:"pitch_cents"`
	Pressure   float64  `json:"pressure"`
	Timbre     float64  `json:"timbre"`
	Remove     bool     `json:"remove,omitempty"`
}

func (SetSharedExpression) Kind() string { return "setsharedexpression" }

func init() {
	Register("setperformednote", func() Intent { return &SetPerformedNote{} })
	Register("resizeperformance", func() Intent { return &ResizePerformance{} })
	Register("setperformedexpression", func() Intent { return &SetPerformedExpression{} })
	Register("setperformancegroup", func() Intent { return &SetPerformanceGroup{} })
	Register("bindperformedgroup", func() Intent { return &BindPerformedGroup{} })
	Register("setsharedexpression", func() Intent { return &SetSharedExpression{} })
	Handle("setperformednote", editPerformance)
	Handle("resizeperformance", editPerformance)
	Handle("setperformedexpression", editPerformance)
	Handle("setperformancegroup", editPerformance)
	Handle("bindperformedgroup", editPerformance)
	Handle("setsharedexpression", editPerformance)
}

func editPerformance(ctx *Context, intent Intent) error {
	var entity EntityID
	switch in := intent.(type) {
	case *SetPerformedNote:
		entity = in.Entity
	case *ResizePerformance:
		entity = in.Entity
	case *ScalePerformance:
		entity = in.Entity
	case *ShapePerformancePitch:
		entity = in.Entity
	case *SetPerformedExpression:
		entity = in.Entity
	case *SetPerformanceGroup:
		entity = in.Entity
	case *BindPerformedGroup:
		entity = in.Entity
	case *SetSharedExpression:
		entity = in.Entity
	}
	id, err := patternTarget(entity)
	if err != nil {
		return err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	var performance *seq.Performance
	var transpose int
	for _, pattern := range plan.Patterns {
		if pattern.ID == id && pattern.Performance != nil {
			copy := *pattern.Performance
			copy.Notes = append([]seq.PerformedNote(nil), copy.Notes...)
			copy.Groups = append([]seq.ControlGroup(nil), copy.Groups...)
			for i := range copy.Groups {
				copy.Groups[i].Points = append([]seq.GroupPoint(nil), copy.Groups[i].Points...)
			}
			for i := range copy.Notes {
				copy.Notes[i].Expressions = append([]seq.PerformedExpression(nil), copy.Notes[i].Expressions...)
			}
			performance = &copy
			transpose = int(pattern.Transpose)
			break
		}
	}
	if performance == nil {
		return fmt.Errorf("pattern %q is not a played-timing performance", id)
	}
	switch in := intent.(type) {
	case *ShapePerformancePitch:
		if err := shapePerformancePitch(performance, transpose, in); err != nil {
			return err
		}
		if err := replaceProjectPerformance(ctx, id, performance, true, true); err != nil {
			return err
		}
		ctx.SetLabel("Performance · " + id + " pitch shaped")
		return nil
	case *ScalePerformance:
		if err := scalePerformance(performance, in.Numerator, in.Denominator); err != nil {
			return err
		}
		if err := replaceProjectPerformance(ctx, id, performance, true, true); err != nil {
			return err
		}
		ctx.SetLabel(fmt.Sprintf("Performance · %s duration scaled %d/%d", id, in.Numerator, in.Denominator))
		return nil
	case *SetPerformanceGroup:
		if in.Index < 0 || in.Index > len(performance.Groups) || in.Remove && in.Index == len(performance.Groups) {
			return fmt.Errorf("shared gesture lane is out of range")
		}
		if in.Remove {
			for _, note := range performance.Notes {
				if int(note.Group) == in.Index+1 {
					return fmt.Errorf("unbind this lane's notes before removing it")
				}
			}
			performance.Groups = append(performance.Groups[:in.Index], performance.Groups[in.Index+1:]...)
			for i := range performance.Notes {
				if int(performance.Notes[i].Group) > in.Index+1 {
					performance.Notes[i].Group--
				}
			}
		} else if in.Index == len(performance.Groups) {
			performance.Groups = append(performance.Groups, seq.ControlGroup{Name: in.Name, Points: []seq.GroupPoint{{Value: expression.Controls{Timbre: .5}}}})
		} else {
			performance.Groups[in.Index].Name = in.Name
		}
	case *BindPerformedGroup:
		if in.NoteIndex < 0 || in.NoteIndex >= len(performance.Notes) || in.Group < 0 || in.Group > len(performance.Groups) {
			return fmt.Errorf("note or shared gesture lane is out of range")
		}
		performance.Notes[in.NoteIndex].Group = uint16(in.Group)
	case *SetSharedExpression:
		if in.GroupIndex < 0 || in.GroupIndex >= len(performance.Groups) {
			return fmt.Errorf("shared gesture lane is out of range")
		}
		group := &performance.Groups[in.GroupIndex]
		if in.Index < 0 || in.Index > len(group.Points) || in.Remove && in.Index == len(group.Points) {
			return fmt.Errorf("shared gesture point is out of range")
		}
		if in.Remove {
			group.Points = append(group.Points[:in.Index], group.Points[in.Index+1:]...)
		} else {
			raw := TakeExpression{Tick: in.Tick, PitchCents: in.PitchCents, Pressure: in.Pressure, Timbre: in.Timbre}
			if err := validateTakeExpression(TakeNote{EndTick: performance.LengthTicks - 1, Expressions: []TakeExpression{raw}}); err != nil {
				return err
			}
			point := seq.GroupPoint{Tick: in.Tick, Value: expression.Controls{PitchCents: float32(in.PitchCents), Pressure: float32(in.Pressure), Timbre: float32(in.Timbre)}}
			if in.Index == len(group.Points) {
				group.Points = append(group.Points, point)
			} else {
				group.Points[in.Index] = point
			}
			sort.Slice(group.Points, func(i, j int) bool { return group.Points[i].Tick < group.Points[j].Tick })
		}
	case *SetPerformedExpression:
		if in.NoteIndex < 0 || in.NoteIndex >= len(performance.Notes) {
			return fmt.Errorf("performed gesture note is out of range")
		}
		note := &performance.Notes[in.NoteIndex]
		if in.Index < 0 || in.Index > len(note.Expressions) || in.Remove && in.Index == len(note.Expressions) {
			return fmt.Errorf("performed gesture sample is out of range")
		}
		if in.Remove {
			note.Expressions = append(note.Expressions[:in.Index], note.Expressions[in.Index+1:]...)
		} else {
			raw := TakeExpression{Tick: in.Tick, PitchCents: in.PitchCents, Pressure: in.Pressure, Timbre: in.Timbre, VibratoDepthCents: &in.VibratoDepthCents}
			if err := validateTakeExpression(TakeNote{EndTick: note.EndTick - note.Tick, Expressions: []TakeExpression{raw}}); err != nil {
				return err
			}
			point := seq.PerformedExpression{Tick: in.Tick, Params: seq.Expression{Set: true, PitchCents: float32(in.PitchCents), Pressure: float32(in.Pressure), Timbre: float32(in.Timbre), VibratoDepthCents: float32(in.VibratoDepthCents)}}
			if math.IsNaN(in.VibratoRateHz) || math.IsInf(in.VibratoRateHz, 0) || in.VibratoRateHz < 0 || in.VibratoRateHz > 100 {
				return fmt.Errorf("gesture vibrato rate must be 0 to 100Hz")
			}
			point.Params.VibratoRateHz = float32(in.VibratoRateHz)
			if in.Index == len(note.Expressions) {
				note.Expressions = append(note.Expressions, point)
			} else {
				note.Expressions[in.Index] = point
			}
			sort.Slice(note.Expressions, func(i, j int) bool { return note.Expressions[i].Tick < note.Expressions[j].Tick })
		}
	case *ResizePerformance:
		performance.LengthTicks = in.LengthTicks
	case *SetPerformedNote:
		if in.Index < 0 || in.Index > len(performance.Notes) || in.Remove && in.Index == len(performance.Notes) {
			return fmt.Errorf("performed note index is out of range")
		}
		if in.Remove {
			performance.Notes = append(performance.Notes[:in.Index], performance.Notes[in.Index+1:]...)
		} else {
			if in.Pitch < 0 || in.Pitch > 127 || in.Velocity < 1 || in.Velocity > 127 {
				return fmt.Errorf("performed pitch must be 0 to 127 and velocity 1 to 127")
			}
			note := seq.PerformedNote{Tick: in.Tick, EndTick: in.EndTick, Note: uint8(in.Pitch), Velocity: uint8(in.Velocity)}
			if in.Index == len(performance.Notes) {
				performance.Notes = append(performance.Notes, note)
			} else {
				note.Expressions = performance.Notes[in.Index].Expressions
				note.Group = performance.Notes[in.Index].Group
				performance.Notes[in.Index] = note
			}
		}
	}
	if err := performance.Validate(); err != nil {
		return err
	}
	if err := replacePerformedPattern(ctx, id, performance, true); err != nil {
		return err
	}
	ctx.SetLabel("Performance · " + id + " edited")
	return nil
}
