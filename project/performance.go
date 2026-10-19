package project

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

type Performance struct {
	LengthTicks int64           `json:"length_ticks" cicada:"Loop length of the played performance" unit:"ticks" range:"30..1099511627776"`
	Notes       []PerformedNote `json:"notes" cicada:"Notes with independent start and end positions" range:"1..512"`
}

type PerformedNote struct {
	Tick        int64                 `json:"tick" cicada:"Inclusive note start" unit:"ticks" range:"0..1099511627775"`
	EndTick     int64                 `json:"end_tick" cicada:"Exclusive note end" unit:"ticks" range:"1..1099511627776"`
	Note        uint8                 `json:"note" cicada:"MIDI pitch" range:"0..127"`
	Velocity    uint8                 `json:"velocity" cicada:"Played note velocity" range:"1..127"`
	Expressions []PerformedExpression `json:"expressions,omitempty" cicada:"Played controller samples relative to this note" range:"0..65536"`
}

type PerformedExpression struct {
	Tick          int64          `json:"tick" cicada:"Controller offset from the note onset" unit:"ticks" range:"0..1099511627776"`
	Value         NoteExpression `json:"value" cicada:"Pitch, pressure, timbre, and vibrato at this position"`
	VibratoRateHz float32        `json:"vibrato_rate_hz" cicada:"Per-note vibrato rate; zero pauses its phase" unit:"Hz" range:"0..100"`
}

func (p *Performance) Kernel() *seq.Performance {
	if p == nil {
		return nil
	}
	out := &seq.Performance{LengthTicks: p.LengthTicks, Notes: make([]seq.PerformedNote, len(p.Notes))}
	for i, note := range p.Notes {
		out.Notes[i] = seq.PerformedNote{Tick: note.Tick, EndTick: note.EndTick, Note: note.Note, Velocity: note.Velocity}
		for _, point := range note.Expressions {
			value := point.Value
			params := seq.Expression{Set: true, PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre, VibratoDepthCents: value.VibratoDepthCents, VibratoRateHz: point.VibratoRateHz}
			out.Notes[i].Expressions = append(out.Notes[i].Expressions, seq.PerformedExpression{Tick: point.Tick, Params: params})
		}
	}
	return out
}

func projectPerformance(p *seq.Performance) *Performance {
	if p == nil {
		return nil
	}
	out := &Performance{LengthTicks: p.LengthTicks, Notes: make([]PerformedNote, len(p.Notes))}
	for i, note := range p.Notes {
		out.Notes[i] = PerformedNote{Tick: note.Tick, EndTick: note.EndTick, Note: note.Note, Velocity: note.Velocity}
		for _, point := range note.Expressions {
			value := point.Params
			out.Notes[i].Expressions = append(out.Notes[i].Expressions, PerformedExpression{Tick: point.Tick, Value: NoteExpression{PitchCents: value.PitchCents, Pressure: value.Pressure, Timbre: value.Timbre, VibratoDepthCents: value.VibratoDepthCents}, VibratoRateHz: value.VibratoRateHz})
		}
	}
	return out
}

func compilePerformance(score *notation.Score, source notation.Pattern) ([]CompiledPattern, error) {
	pattern := seq.Pattern{Len: 1, GatePercent: 55, Seed: uint32(score.Seed), Performance: source.Performance}
	for _, attribute := range source.Attrs {
		switch attribute.Name {
		case "length", "slot":
		case "transpose":
			value, err := strconv.Atoi(attribute.Value)
			if err != nil || value < -24 || value > 24 {
				return nil, fmt.Errorf("performance transpose must be -24 to 24 semitones")
			}
			pattern.Transpose = int8(value)
		case "seed":
			value, err := strconv.ParseUint(attribute.Value, 10, 32)
			if err != nil {
				return nil, err
			}
			pattern.Seed = uint32(value)
		default:
			return nil, fmt.Errorf("performance pattern %s uses played timing; %s is a step-grid setting", source.Name, attribute.Name)
		}
	}
	if err := pattern.Validate(); err != nil {
		return nil, err
	}
	return []CompiledPattern{{Name: source.Name, Kind: "notes", Pattern: pattern}}, nil
}

func (p *Pattern) DurationTicks() int64 {
	if p.Performance != nil {
		return p.Performance.LengthTicks
	}
	grid := int64(p.StepTicks)
	if grid == 0 {
		grid = seq.TicksPerStep
	}
	return int64(p.Steps) * grid
}

func performanceSource(pattern Pattern, slot int, assigned bool, projectSeed uint32) string {
	var out strings.Builder
	fmt.Fprintf(&out, "pattern %s performance length=%dticks", pattern.ID, pattern.Performance.LengthTicks)
	if pattern.Transpose != 0 {
		fmt.Fprintf(&out, " transpose=%d", pattern.Transpose)
	}
	if pattern.Seed != projectSeed {
		fmt.Fprintf(&out, " seed=%d", pattern.Seed)
	}
	if assigned {
		fmt.Fprintf(&out, " slot=%d", slot)
	}
	out.WriteString(" {\n")
	for _, note := range pattern.Performance.Kernel().Notes {
		out.WriteString(notation.PerformedNoteSource(note))
	}
	out.WriteString("}\n")
	return out.String()
}

func validatePerformedTrack(p *Project, track Track, pattern Pattern) error {
	if pattern.Performance == nil {
		return nil
	}
	for _, kit := range p.Kits {
		if kit.ID == track.Kind {
			return fmt.Errorf("performed notes need a pitched instrument on track %s", track.ID)
		}
	}
	for _, sampler := range p.Samplers {
		if sampler.Name == track.Kind {
			return fmt.Errorf("performed notes for sampler tracks are not supported yet")
		}
	}
	modeled := isModeledPiano(p, track.Kind) || isModeledKeys(p, track.Kind)
	pitched := track.Kind == "acid" || modeled
	for _, instrument := range p.Instruments {
		if instrument.ID == track.Kind {
			pitched = true
		}
	}
	if !pitched {
		return fmt.Errorf("performed notes need a pitched instrument on track %s", track.ID)
	}
	if track.Kind != "acid" {
		authored := false
		for _, instrument := range p.Instruments {
			authored = authored || instrument.ID == track.Kind
		}
		if !authored {
			for _, note := range pattern.Performance.Notes {
				if len(note.Expressions) != 0 {
					return fmt.Errorf("CICADA-UNSUPPORTED: performed expression needs an acid or authored synth voice on track %s", track.ID)
				}
			}
		}
	}
	if modeled {
		for _, note := range pattern.Performance.Notes {
			pitch := int(note.Note) + int(pattern.Transpose)
			if pitch < 21 || pitch > 108 {
				return fmt.Errorf("track %s pattern %s: keyboard notes must be MIDI 21 to 108", track.ID, pattern.ID)
			}
		}
	}
	return nil
}
