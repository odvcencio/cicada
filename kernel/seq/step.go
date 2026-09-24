package seq

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
type Pattern struct {
	Steps         [64]uint32
	Len           uint8
	SwingPermille uint16 // fraction of one step, 0..500
	Transpose     int8
	GatePercent   uint8
	Seed          uint32
}

func (p *Pattern) Validate() error {
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
	for i := uint8(0); i < p.Len; i++ {
		if _, err := UnpackStep(p.Steps[i]); err != nil {
			return err
		}
	}
	return nil
}
