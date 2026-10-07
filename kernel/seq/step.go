package seq

import "m31labs.dev/cicada/kernel/expression"

// Expression is the resolved per-step expression shared by the score and voices.
type Expression = expression.Params

// Step is the unpacked form of Cicada's 32-bit wire-format step.
type Step struct {
	Note        uint8
	Accent      bool
	Slide       bool
	Gate        bool
	Tie         bool
	Ratchet     uint8 // 1..8
	Probability uint8 // 0..100
	Velocity    uint8 // drums; acid ignores this field
}

func (s Step) Validate() error {
	if s.Ratchet < 1 || s.Ratchet > 8 {
		return Error("ratchet must be 1 to 8")
	}
	if s.Probability > 100 {
		return Error("probability must be 0 to 100")
	}
	if s.Tie && !s.Gate {
		return Error("tie requires gate")
	}
	if s.Tie && s.Ratchet != 1 {
		return Error("tie cannot ratchet")
	}
	return nil
}

func PackStep(s Step) (uint32, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	v := uint32(s.Note) | uint32(s.Ratchet-1)<<11 | uint32(s.Probability)<<14 | uint32(s.Velocity)<<21
	if s.Accent {
		v |= 1 << 7
	}
	if s.Slide {
		v |= 1 << 8
	}
	if s.Gate {
		v |= 1 << 9
	}
	if s.Tie {
		v |= 1 << 10
	}
	return v, nil
}

func UnpackStep(v uint32) (Step, error) {
	if v>>28 != 0 {
		return Step{}, Error("reserved step bits must be zero")
	}
	s := Step{
		Note: uint8(v & 0x7f), Accent: v&(1<<7) != 0,
		Slide: v&(1<<8) != 0, Gate: v&(1<<9) != 0,
		Tie: v&(1<<10) != 0, Ratchet: uint8((v>>11)&7) + 1,
		Probability: uint8((v >> 14) & 0x7f), Velocity: uint8((v >> 21) & 0x7f),
	}
	return s, s.Validate()
}

// Pattern is the fixed-size kernel representation. Slots and tracks are held
// by the engine; this type describes one active pattern only.
// ChordStep is an additive payload; Count zero retains the mono packed step.
// A chord shares one gate, probability decision and deterministic cohort ID.
type ChordStep struct {
	Notes [4]uint8
	Count uint8
}

func (c ChordStep) Validate(step Step) error {
	if c.Count == 0 {
		if c.Notes != [4]uint8{} {
			return Error("unused chord pitches must be zero")
		}
		return nil
	}
	if c.Count < 2 || c.Count > 4 || !step.Gate || step.Tie || step.Slide || step.Ratchet != 1 || c.Notes[0] != step.Note {
		return Error("chord needs 2 to 4 pitches, a gate, and no tie, slide or ratchet")
	}
	for i := uint8(0); i < 4; i++ {
		if i >= c.Count {
			if c.Notes[i] != 0 {
				return Error("unused chord pitches must be zero")
			}
			continue
		}
		if c.Notes[i] > 127 {
			return Error("chord pitch is out of range")
		}
		for j := uint8(0); j < i; j++ {
			if c.Notes[j] == c.Notes[i] {
				return Error("chord pitches must be distinct")
			}
		}
	}
	return nil
}

type Pattern struct {
	StepTicks uint16 `json:",omitempty"` // zero keeps the sixteenth-note grid
	// Expression is immutable once loaded by the engine; nil is neutral.
	Expression    *[64]Expression `json:",omitempty"`
	Chords        [64]ChordStep   `json:",omitzero"`
	Steps         [64]uint32
	Len           uint8
	SwingPermille uint16 // fraction of one step, 0..500
	Transpose     int8
	GatePercent   uint8
	Seed          uint32
}

// GridTicks returns the duration of one cell.
//
//go:noinline
func (p *Pattern) GridTicks() int64 {
	if p.StepTicks == 0 {
		return TicksPerStep
	}
	return int64(p.StepTicks)
}

func (p *Pattern) Validate() error {
	if p.StepTicks != 0 && (p.StepTicks < 30 || p.StepTicks > 3840) {
		return Error("grid must be 30 to 3840 ticks")
	}
	if p.Len < 1 || p.Len > 64 {
		return Error("pattern length must be 1 to 64")
	}
	if p.SwingPermille > 500 {
		return Error("swing must be 0 to 500 permille")
	}
	if p.Transpose < -24 || p.Transpose > 24 {
		return Error("transpose must be -24 to 24 semitones")
	}
	if p.GatePercent < 10 || p.GatePercent > 100 {
		return Error("gate must be 10 to 100 percent")
	}
	for i := int(p.Len); i < 64; i++ {
		if p.Chords[i] != (ChordStep{}) {
			return Error("chord payload exceeds pattern length")
		}
	}
	for i := uint8(0); i < p.Len; i++ {
		if err := p.ExpressionAt(int(i)).Validate(); err != nil {
			return err
		}
		step, err := UnpackStep(p.Steps[i])
		if err != nil {
			return err
		}
		if err := p.Chords[i].Validate(step); err != nil {
			return err
		}
		if p.Chords[i].Count > 0 {
			prev, _ := UnpackStep(p.Steps[(int(i)+int(p.Len)-1)%int(p.Len)])
			if prev.Slide {
				return Error("a slide cannot target a chord")
			}
		}
		for n := uint8(0); n < p.Chords[i].Count; n++ {
			if int(p.Chords[i].Notes[n])+int(p.Transpose) < 0 || int(p.Chords[i].Notes[n])+int(p.Transpose) > 127 {
				return Error("transposed chord pitch is out of range")
			}
		}
		if step.Gate && !step.Tie && (int(step.Note)+int(p.Transpose) < 0 || int(step.Note)+int(p.Transpose) > 127) {
			return Error("transposed note is out of range")
		}
	}
	return nil
}

// ExpressionAt reads optional score expression without allocating. The voice
// resolves an unset value to its neutral defaults when it receives the value.
func (p *Pattern) ExpressionAt(index int) Expression {
	if p.Expression == nil || index < 0 || index >= len(p.Steps) {
		return Expression{}
	}
	return p.Expression[index]
}
