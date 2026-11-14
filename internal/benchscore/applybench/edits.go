package applybench

import (
	"fmt"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/benchscore"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// The hot edits, in report order: four of the five the revision times
// (section E.2; the mixer level is not in this set) and one structural edit.
const (
	ToggleStep = "togglestep"
	SetPitch   = "setpitch"
	MoveNotes  = "editnotes move"
	MovePlace  = "placement move"
	AddScene   = "addscene"
)

// EditNames lists the hot edits in report order.
var EditNames = []string{ToggleStep, SetPitch, MoveNotes, MovePlace, AddScene}

// movedNotes is how many notes the EditNotes move names when the pattern
// holds that many.
const movedNotes = 16

// HotEdit is one edit as Studio sends it.
type HotEdit struct {
	Name   string
	Intent edit.Intent
	// Note says how the edit differs from its definition on this reference,
	// such as a move of fewer than 16 notes; "" when it does not.
	Note string
	// Missing says why the reference has no such edit (REF-S has no
	// placements); Intent is nil then.
	Missing string
}

// HotEdits builds the hot edits for reference from base, the full compile of
// its source: the entities come from the reference, the notes and positions
// from the compiled plan, so each edit names what the score really holds.
func HotEdits(reference benchscore.Reference, base *State) ([]HotEdit, error) {
	plan := base.Plan
	step, err := findPattern(plan, reference.StepPattern)
	if err != nil {
		return nil, err
	}
	pitch := 62
	if len(step.Data) > 0 && step.Data[0] != nil {
		pitch = int(step.Data[0].Note) + 2
	}
	edits := []HotEdit{
		// Studio's step route sends shared "definition" (cmd/cicada/studio.go).
		{Name: ToggleStep, Intent: &edit.ToggleStep{Entity: edit.EntityID("step:" + step.ID + "/1"), Shared: "definition"}},
		{Name: SetPitch, Intent: &edit.SetPitch{Entity: edit.EntityID("step:" + step.ID + "/0"), Pitch: pitch, Shared: "definition"}},
	}

	notes, err := findPattern(plan, reference.NotesPattern)
	if err != nil {
		return nil, err
	}
	refs := noteRefs(notes, movedNotes)
	move := HotEdit{Name: MoveNotes, Intent: &edit.EditNotes{Entity: edit.EntityID("pattern:" + notes.ID), Op: "move", DP: 1, Notes: refs}}
	switch {
	case len(refs) == 0:
		return nil, fmt.Errorf("%s: pattern %s holds no notes to move", reference.Name, notes.ID)
	case len(refs) < movedNotes:
		move.Note = fmt.Sprintf("%d notes, all that %s holds", len(refs), notes.ID)
	}
	edits = append(edits, move)

	if reference.Placement == "" {
		edits = append(edits, HotEdit{Name: MovePlace, Missing: "no placements"})
	} else {
		var placement *edit.Placement
		for i := range plan.Placements {
			if plan.Placements[i].ID == reference.Placement {
				placement = &plan.Placements[i]
			}
		}
		if placement == nil {
			return nil, fmt.Errorf("%s: no placement %s", reference.Name, reference.Placement)
		}
		// One beat later, as Studio's placement route sends it.
		meter := songMeter(base)
		bar, _ := meter.BarAt(placement.AtTick)
		edits = append(edits, HotEdit{Name: MovePlace, Intent: &edit.UpdatePlacement{
			Entity: edit.EntityID("placement:" + placement.ID), Settings: &edit.PlacementSettings{At: position(meter, placement.AtTick+meter.BeatTicks(bar))},
		}})
	}
	edits = append(edits, HotEdit{Name: AddScene, Intent: &edit.AddScene{Name: "bench-scene"}})
	return edits, nil
}

func findPattern(plan *edit.Plan, id string) (*edit.Pattern, error) {
	for i := range plan.Patterns {
		if plan.Patterns[i].ID == id {
			return &plan.Patterns[i], nil
		}
	}
	return nil, fmt.Errorf("the reference has no pattern %q", id)
}

// noteRefs names up to limit notes of a pattern as Studio's note editor sees
// them: a performance's notes from its middle, a step pattern's onsets in
// order (start and length in ticks, held steps included in the length).
func noteRefs(pattern *edit.Pattern, limit int) []edit.NoteRef {
	var refs []edit.NoteRef
	if pattern.Performance != nil {
		notes := pattern.Performance.Notes
		first := max(0, len(notes)/2-limit/2)
		for _, note := range notes[first:min(len(notes), first+limit)] {
			refs = append(refs, edit.NoteRef{At: note.Tick, Pitch: int(note.Note), Length: note.EndTick - note.Tick, Velocity: int(note.Velocity)})
		}
		return refs
	}
	stepTicks := pattern.StepTicksOrDefault()
	for i := 0; i < len(pattern.Data) && len(refs) < limit; i++ {
		step := pattern.Data[i]
		if step == nil || step.Tie {
			continue
		}
		length := int64(1)
		for i+int(length) < len(pattern.Data) && pattern.Data[i+int(length)] != nil && pattern.Data[i+int(length)].Tie {
			length++
		}
		pitches := step.Notes
		if len(pitches) == 0 {
			pitches = []int{int(step.Note)}
		}
		for _, pitch := range pitches {
			if len(refs) < limit {
				refs = append(refs, edit.NoteRef{At: int64(i) * stepTicks, Pitch: pitch, Length: length * stepTicks, Velocity: int(step.Velocity)})
			}
		}
	}
	return refs
}

// songMeter returns the song's meter map: the compiled project's, or the
// parsed score's when the build cannot compile it.
func songMeter(base *State) seq.MeterMap {
	if base.Project != nil {
		return base.Project.MeterMap()
	}
	return base.Score.MeterMap()
}

// position spells a tick on a step as @bar.beat.step.
func position(meter seq.MeterMap, tick int64) string {
	bar, beat, step, _ := meter.Position(tick)
	return fmt.Sprintf("@%d.%d.%d", bar, beat, step)
}

// HotEditsFromSource builds reference's hot edits from its parsed score
// alone, for a build that cannot compile it (REF-L under portable limits, where the
// portable limits allow 16 tracks): patterns, notes and placements come from
// the notation.Score, and setpitch sets d4. Every such edit is refused by its
// first compile, so only the time to the refusal is measured.
func HotEditsFromSource(reference benchscore.Reference) ([]HotEdit, error) {
	score, _ := notation.Parse(reference.Source)
	if score == nil {
		return nil, fmt.Errorf("%s does not parse", reference.Name)
	}
	plan := &edit.Plan{}
	for _, pattern := range score.Patterns {
		plan.Patterns = append(plan.Patterns, edit.Pattern{ID: pattern.Name, Kind: pattern.Kind, Performance: pattern.Performance})
	}
	if score.Arrange != nil {
		for _, p := range score.Arrange.Placements {
			plan.Placements = append(plan.Placements, edit.Placement{ID: p.Name, Track: p.Track, Content: p.Content, AtTick: p.AtTick, LengthTicks: p.LengthTicks})
		}
	}
	return HotEdits(reference, &State{Score: score, Plan: plan})
}
