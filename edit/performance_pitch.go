package edit

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
)

// ShapePerformancePitch changes note pitches while retaining their articulation.
// Pivot is a stored MIDI pitch, before the pattern's transpose setting.
//
// Example: {"kind":"shapeperformancepitch","entity":"pattern:phrase","operation":"transpose","semitones":12}
// Example: {"kind":"shapeperformancepitch","entity":"pattern:phrase","operation":"reflect","pivot":60}
type ShapePerformancePitch struct {
	Entity    EntityID `json:"entity" cicada:"required"`                                        // the performance pattern, as pattern:<name>
	Operation string   `json:"operation" values:"pitch-operation" cicada:"required,selects"`    // shift every note, or mirror the notes around the pivot
	Semitones int      `json:"semitones,omitempty" when:"operation=transpose" unit:"semitones"` // the shift for transpose, −48 to +48 and not 0
	Pivot     int      `json:"pivot,omitempty" when:"operation=reflect"`                        // the MIDI pitch to mirror around, for reflect
}

func (ShapePerformancePitch) Kind() string { return "shapeperformancepitch" }

func init() {
	Register("shapeperformancepitch", func() Intent { return &ShapePerformancePitch{} })
	Handle("shapeperformancepitch", editPerformance)
}

// pitchOperations are what ShapePerformancePitch's operation accepts.
var pitchOperations = valueList("pitch-operation", "transpose", "reflect")

func shapePerformancePitch(p *seq.Performance, transpose int, in *ShapePerformancePitch) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !containsString(pitchOperations, in.Operation) {
		return fmt.Errorf("choose transpose or reflect for pitch shaping")
	}
	switch in.Operation {
	case "transpose":
		if in.Semitones < -48 || in.Semitones > 48 || in.Semitones == 0 || in.Pivot != 0 {
			return fmt.Errorf("choose a nonzero shift from −48 to +48 semitones")
		}
	case "reflect":
		if in.Pivot < 0 || in.Pivot > 127 || in.Semitones != 0 {
			return fmt.Errorf("mirror center must be a MIDI pitch from 0 to 127")
		}
	default:
		return fmt.Errorf("choose transpose or reflect for pitch shaping")
	}
	// Check the entire result before changing any note; never clamp or wrap a
	// pitch. Include the retained transpose even for an unassigned performance.
	pitches := make([]uint8, len(p.Notes))
	for i, note := range p.Notes {
		pitch := int(note.Note) + in.Semitones
		if in.Operation == "reflect" {
			pitch = 2*in.Pivot - int(note.Note)
		}
		if pitch < 0 || pitch > 127 || pitch+transpose < 0 || pitch+transpose > 127 {
			return fmt.Errorf("pitch shaping would put note %d outside MIDI 0–127; choose a smaller shift or another mirror center", i+1)
		}
		pitches[i] = uint8(pitch)
	}
	for i, pitch := range pitches {
		p.Notes[i].Note = pitch
	}
	return nil
}
