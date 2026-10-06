package phrase

import (
	"fmt"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// Mutate applies seeded operations to a generated bar, leaving base unchanged.
// Params supplies the tonal context and 64-bit mutation seed. The bar supplies
// its length, swing and gate. Locked is a bitmask of immutable step positions.
// If a repair would change a locked step, no result is returned.
//
// This API accepts the generator's acid wire format: untransposed notes in its
// pitch palette, with probability and velocity 100. Other step formats are
// rejected rather than silently losing expression in the source projection.
func Mutate(params Params, base seq.Pattern, ops []Op, locked uint64) (Result, error) {
	if err := base.Validate(); err != nil {
		return Result{}, err
	}
	if base.Len < 64 && locked>>base.Len != 0 {
		return Result{}, fmt.Errorf("locked mask exceeds the bar length")
	}
	if len(ops) > 64 {
		return Result{}, fmt.Errorf("at most 64 mutation operations are allowed")
	}
	if base.Transpose != 0 {
		return Result{}, fmt.Errorf("phrase mutation needs an untransposed generator bar")
	}
	params.Steps, params.GatePercent, params.Structure = base.Len, base.GatePercent, A
	params.SwingPercent100 = 5000 + base.SwingPermille*5
	p, err := normalize(params)
	if err != nil {
		return Result{}, err
	}
	notes, err := decodeGeneratorBar(base, p)
	if err != nil {
		return Result{}, err
	}
	changed, trace, err := mutateNotes(notes, p.Seed, ops, locked, p)
	if err != nil {
		return Result{}, err
	}
	bar, err := encodeBar(changed, p)
	if err != nil {
		return Result{}, err
	}
	for i := uint8(0); i < base.Len; i++ {
		if locked&(uint64(1)<<i) != 0 && base.Steps[i] != bar.Steps[i] {
			return Result{}, fmt.Errorf("CICADA-LOCKED: repair would change step %d", i)
		}
	}
	source, err := sourceForBar(changed, p)
	if err != nil {
		return Result{}, err
	}
	document, err := notation.ParseDocument([]byte(source))
	if err != nil {
		return Result{}, err
	}
	formatted, err := notation.Format(document)
	if err != nil {
		return Result{}, err
	}
	return Result{Bars: []seq.Pattern{bar}, Notation: string(formatted), Trace: trace}, nil
}

// Evolve makes one deterministic variation for a generation index. The same
// base, params, generation and lock mask produce the same source and trace.
// It selects three operations outside the audio callback; generation zero is
// a valid first variation. Rotations are excluded when any step is locked.
func Evolve(params Params, base seq.Pattern, generation uint64, locked uint64) (Result, error) {
	params.Seed ^= (generation + 1) * 0x9e3779b97f4a7c15
	stream := newStream(params.Seed)
	kinds := []OpKind{NudgeDegree, ToggleAccent, ToggleSlide, OctaveFlip, SwapSteps, FillRest, Thin, Ratchet}
	if locked == 0 {
		kinds = append(kinds, RotateRhythm)
	}
	ops := make([]Op, 3)
	for i := range ops {
		ops[i] = Op{Kind: kinds[stream.choose("evolve", i, len(kinds))], Arg: 2}
	}
	result, err := Mutate(params, base, ops, locked)
	if err != nil {
		return Result{}, err
	}
	result.Trace = append(stream.trace, result.Trace...)
	return result, nil
}

func decodeGeneratorBar(base seq.Pattern, p normalizedParams) ([]noteState, error) {
	root := 12*(int(p.RootOctave)+1) + int(p.Key)
	notes := make([]noteState, base.Len)
	for i := range notes {
		step, err := seq.UnpackStep(base.Steps[i])
		if err != nil {
			return nil, err
		}
		if step.Probability != 100 || step.Velocity != 100 {
			return nil, fmt.Errorf("step %d is outside the generator's expression format", i)
		}
		if !step.Gate {
			continue
		}
		note := noteState{active: true, accent: step.Accent, slide: step.Slide, tie: step.Tie, note: int(step.Note), ratchet: step.Ratchet}
		if step.Tie {
			notes[i] = note
			continue
		}
		found := false
		for class := classRoot; class <= classSeven; class++ {
			if (int(step.Note)-root-classOffsets[p.Scale][class])%12 == 0 {
				note.class = class
				found = true
				break
			}
		}
		if !found && p.Scale == Blues && (int(step.Note)-root-6)%12 == 0 {
			note.class = classFour
			found = true
		}
		if !found {
			return nil, fmt.Errorf("step %d pitch is outside the generator's palette", i)
		}
		if i > 0 && int(step.Note) == root+12 {
			note.class = classOctave
		}
		note.raised = int(step.Note) >= root+12
		notes[i] = note
	}
	// Ties carry their preceding pitch for interval repair and slide eligibility.
	for i := range notes {
		if !notes[i].tie {
			continue
		}
		for distance := 1; distance < len(notes); distance++ {
			previous := notes[(i+len(notes)-distance)%len(notes)]
			if previous.active && !previous.tie {
				notes[i].note, notes[i].class = previous.note, previous.class
				break
			}
		}
	}
	return notes, nil
}
