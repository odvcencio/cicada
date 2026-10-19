package expression

// Group names a shared control lane within a score generation or live input
// session. Zero is unbound; reusing a slot never inherits its previous group.
type Group struct {
	ID         uint16
	Generation uint64
}

// Controls is a complete shared snapshot. Pitch adds to a note's bend,
// pressure uses the larger value, and centered timbre adds before clamping.
// A shared lane does not restart or replace the note's own vibrato.
type Controls struct {
	PitchCents, Pressure, Timbre float32
}

func (c Controls) Validate() error {
	return (Params{PitchCents: c.PitchCents, Pressure: c.Pressure, Timbre: c.Timbre, Set: true}).Validate()
}

func (c Controls) Apply(p Params) Params {
	p.PitchCents = max(-9600, min(9600, p.PitchCents+c.PitchCents))
	p.Pressure = max(p.Pressure, c.Pressure)
	p.Timbre = max(0, min(1, p.Timbre+c.Timbre-.5))
	return p
}

// SetGroupControls changes the shared layer even while the gate is released.
// The caller owns group identity and voice lifetime checks.
func (s *State) SetGroupControls(c Controls) {
	s.shared, s.grouped = c, true
	s.resolve()
}

func (s *State) resolve() {
	s.Params = s.member
	if s.grouped {
		s.Params = s.shared.Apply(s.Params)
	}
	s.updateRatio()
}
