package edit

import (
	"fmt"

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

func init() {
	Register("setperformednote", func() Intent { return &SetPerformedNote{} })
	Register("resizeperformance", func() Intent { return &ResizePerformance{} })
	Handle("setperformednote", editPerformance)
	Handle("resizeperformance", editPerformance)
}

func editPerformance(ctx *Context, intent Intent) error {
	var entity EntityID
	switch in := intent.(type) {
	case *SetPerformedNote:
		entity = in.Entity
	case *ResizePerformance:
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
	for _, pattern := range plan.Patterns {
		if pattern.ID == id && pattern.Performance != nil {
			copy := *pattern.Performance
			copy.Notes = append([]seq.PerformedNote(nil), copy.Notes...)
			performance = &copy
			break
		}
	}
	if performance == nil {
		return fmt.Errorf("pattern %q is not a played-timing performance", id)
	}
	switch in := intent.(type) {
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
