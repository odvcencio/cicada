package edit

import (
	"fmt"
	"math"
	"sort"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

// SetPerformedNote edits, adds or removes one note of a performance pattern.
// Index equal to the note count appends. Pitch is the stored MIDI pitch,
// before an authored pattern transpose.
//
// Example: {"kind":"setperformednote","entity":"pattern:phrase","index":1,"tick":960,"end_tick":1920,"pitch":62,"velocity":100}
type SetPerformedNote struct {
	Entity   EntityID `json:"entity" cicada:"required"`                // the performance pattern, as pattern:<name>
	Index    int      `json:"index"`                                   // the note's index, from 0, in start order
	Tick     int64    `json:"tick" unit:"ticks"`                       // the note's start in the loop, 960 per beat
	EndTick  int64    `json:"end_tick" unit:"ticks" cicada:"required"` // the note's end
	Pitch    int      `json:"pitch"`                                   // MIDI pitch 0–127
	Velocity int      `json:"velocity" cicada:"required"`              // MIDI velocity 1–127
	Remove   bool     `json:"remove,omitempty"`                        // remove the note instead
}

func (SetPerformedNote) Kind() string { return "setperformednote" }

// ResizePerformance changes the loop length of a performance pattern. Notes
// keep their ticks; a resize that would cut a note or gesture is refused.
//
// Example: {"kind":"resizeperformance","entity":"pattern:phrase","length_ticks":7680}
type ResizePerformance struct {
	Entity      EntityID `json:"entity" cicada:"required"`                    // the performance pattern, as pattern:<name>
	LengthTicks int64    `json:"length_ticks" unit:"ticks" cicada:"required"` // the new loop length, 30 ticks or more
}

func (ResizePerformance) Kind() string { return "resizeperformance" }

// SetPerformedExpression edits one note-owned controller sample. Index equal
// to the sample count appends; edits preserve every other note and gesture.
//
// Example: {"kind":"setperformedexpression","entity":"pattern:phrase","note_index":0,"index":1,"tick":240,"pitch_cents":50,"pressure":0.6,"timbre":0.7,"vibrato_depth_cents":0,"vibrato_rate_hz":5}
type SetPerformedExpression struct {
	Entity            EntityID `json:"entity" cicada:"required"`            // the performance pattern, as pattern:<name>
	NoteIndex         int      `json:"note_index"`                          // the note's index, from 0
	Index             int      `json:"index"`                               // the sample's index within the note, from 0
	Tick              int64    `json:"tick" unit:"ticks" cicada:"required"` // the sample's time from the note's start, within its held length
	PitchCents        float64  `json:"pitch_cents" unit:"cents"`            // bend, −9600 to 9600
	Pressure          float64  `json:"pressure"`                            // 0 to 1
	Timbre            float64  `json:"timbre"`                              // 0 to 1
	VibratoDepthCents float64  `json:"vibrato_depth_cents" unit:"cents"`    // 0 to 9600
	VibratoRateHz     float64  `json:"vibrato_rate_hz" unit:"Hz"`           // 0 to 100
	Remove            bool     `json:"remove,omitempty"`                    // remove the sample instead
}

func (SetPerformedExpression) Kind() string { return "setperformedexpression" }

// SetPerformanceGroup adds, renames or removes one shared gesture lane of a
// performance pattern. Index equal to the lane count appends.
//
// Example: {"kind":"setperformancegroup","entity":"pattern:phrase","index":1,"name":"swell"}
type SetPerformanceGroup struct {
	Entity EntityID `json:"entity" cicada:"required"` // the performance pattern, as pattern:<name>
	Index  int      `json:"index"`                    // the lane's index, from 0
	Name   string   `json:"name" cicada:"required"`   // the lane's name
	Remove bool     `json:"remove,omitempty"`         // remove the lane instead
}

func (SetPerformanceGroup) Kind() string { return "setperformancegroup" }

// BindPerformedGroup binds one note of a performance pattern to a shared
// gesture lane, or unbinds it.
//
// Example: {"kind":"bindperformedgroup","entity":"pattern:phrase","note_index":1,"group":1}
type BindPerformedGroup struct {
	Entity    EntityID `json:"entity" cicada:"required"` // the performance pattern, as pattern:<name>
	NoteIndex int      `json:"note_index"`               // the note's index, from 0
	Group     int      `json:"group"`                    // the lane's index counted from 1; 0 unbinds the note
}

func (BindPerformedGroup) Kind() string { return "bindperformedgroup" }

// SetSharedExpression edits, adds or removes one point of a shared gesture
// lane. Index equal to the point count appends.
//
// Example: {"kind":"setsharedexpression","entity":"pattern:phrase","group_index":0,"index":1,"tick":960,"pitch_cents":100,"pressure":0.8,"timbre":0.9}
type SetSharedExpression struct {
	Entity     EntityID `json:"entity" cicada:"required"`            // the performance pattern, as pattern:<name>
	GroupIndex int      `json:"group_index"`                         // the lane's index, from 0
	Index      int      `json:"index"`                               // the point's index, from 0
	Tick       int64    `json:"tick" unit:"ticks" cicada:"required"` // the point's time in the loop, before its end
	PitchCents float64  `json:"pitch_cents" unit:"cents"`            // bend, −9600 to 9600
	Pressure   float64  `json:"pressure"`                            // 0 to 1
	Timbre     float64  `json:"timbre"`                              // 0 to 1
	Remove     bool     `json:"remove,omitempty"`                    // remove the point instead
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
		if err := replaceProjectPerformance(ctx, id, performance, true); err != nil {
			return err
		}
		ctx.SetLabel("Performance · " + id + " pitch shaped")
		return nil
	case *ScalePerformance:
		if err := scalePerformance(performance, in.Numerator, in.Denominator); err != nil {
			return err
		}
		if err := replaceProjectPerformance(ctx, id, performance, true); err != nil {
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
	if err := replaceProjectPerformance(ctx, id, performance, true); err != nil {
		return err
	}
	ctx.SetLabel("Performance · " + id + " edited")
	return nil
}
