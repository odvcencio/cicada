package graph

// LFOClock is a song position supplied by the owning engine before a frame.
// A stopped clock holds song-locked oscillators in place.
type LFOClock struct {
	Sample  uint64
	Seed    uint32
	Playing bool
}

type lfoVoice struct {
	clock       *LFOClock
	rates       []uint64 // integer microhertz, prepared outside rendering
	noteSample  uint64
	localSample uint64
}

// BindLFOClock supplies the transport clock for graph-local oscillators.
// A standalone voice with no clock uses its own advancing sample position.
func (v *Voice) BindLFOClock(clock *LFOClock) {
	if v.lfo != nil {
		v.lfo.clock = clock
	}
}

// HasLFO reports whether a graph needs the transport clock.
func HasLFO(program *Program) bool {
	if program == nil || int(program.Len) > len(program.Nodes) {
		return false
	}
	for _, node := range program.Nodes[:program.Len] {
		if node.Op == LFO {
			return true
		}
	}
	return false
}

// BindLFOClock attaches the same read-only frame clock to every pool voice.
func (p *Pool) BindLFOClock(clock *LFOClock) {
	for i := range p.slots {
		p.slots[i].voice.BindLFOClock(clock)
	}
}

// BindLFOClock attaches the same read-only frame clock to every legacy voice.
func (p *Poly) BindLFOClock(clock *LFOClock) {
	for i := range p.slots {
		p.slots[i].voice.BindLFOClock(clock)
	}
}
