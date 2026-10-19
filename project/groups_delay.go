package project

import (
	"math"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
)

func validateSharedDelay(program graph.Program, sampleRate int, pattern seq.Pattern) error {
	if err := pattern.Performance.Validate(); err != nil {
		return err
	}
	check := func(note uint8, member seq.Expression, shared expression.Controls) error {
		resolved := shared.Apply(member)
		depth := float32(0)
		if resolved.VibratoRateHz != 0 {
			depth = resolved.VibratoDepthCents
		}
		for _, cents := range [...]float32{resolved.PitchCents - depth, resolved.PitchCents + depth} {
			pitch := float32(440 * math.Exp2(float64(int(note)+int(pattern.Transpose)-69)/12+float64(cents)/1200))
			if err := graph.ValidateDelayPitch(program, sampleRate, pitch); err != nil {
				return err
			}
		}
		return nil
	}
	p := pattern.Performance
	for _, note := range p.Notes {
		member := seq.Expression{Timbre: .5}
		shared := expression.Controls{Timbre: .5}
		var group *seq.ControlGroup
		if note.Group != 0 {
			group = &p.Groups[note.Group-1]
			shared = group.At(note.Tick)
		}
		mi, gi := 0, 0
		if group != nil {
			for gi < len(group.Points) && group.Points[gi].Tick <= note.Tick {
				gi++
			}
		}
		at := note.Tick
		for {
			for mi < len(note.Expressions) && note.Tick+note.Expressions[mi].Tick <= at {
				member = note.Expressions[mi].Params
				mi++
			}
			if group != nil {
				for gi < len(group.Points) && group.Points[gi].Tick <= at {
					shared = group.Points[gi].Value
					gi++
				}
			}
			if err := check(note.Note, member, shared); err != nil {
				return err
			}
			next := note.EndTick + 1
			if mi < len(note.Expressions) {
				next = min(next, note.Tick+note.Expressions[mi].Tick)
			}
			if group != nil && gi < len(group.Points) {
				next = min(next, group.Points[gi].Tick)
			}
			if next > note.EndTick {
				break
			}
			at = next
		}
		if group != nil {
			// Releases can span later loop iterations. The frozen member value
			// must be safe under every possible later shared snapshot, including
			// the implicit neutral reset at the start of a loop.
			if err := check(note.Note, member, expression.Controls{Timbre: .5}); err != nil {
				return err
			}
			for _, point := range group.Points {
				if err := check(note.Note, member, point.Value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
