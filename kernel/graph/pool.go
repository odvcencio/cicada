package graph

import "m31labs.dev/cicada/kernel/expression"

// MaxPolyphony is the experimental per-track limit. Storage is fixed at load.
const MaxPolyphony = 4

// Cohort identifies one sequencer onset across pattern generations.
// All pitches share a gate. A stale release cannot affect a replacement onset.
type Cohort struct {
	NoteID     int64
	Generation uint64
}

type Handle struct {
	Slot uint8
	ID   uint64
}

type poolSlot struct {
	voice            *Voice
	id               uint64
	cohort           Cohort
	gated            bool
	release          int
	last, tail, peak float32
	tailLeft         int
}

// Pool sums four graph slots in ascending order. Allocation picks the lowest
// free slot, then the quietest releasing slot, then the oldest gated slot.
// A steal has a single bounded 5 ms declick tail, never an additional voice.
type Pool struct {
	slots                     [MaxPolyphony]poolSlot
	serial                    uint64
	releaseFrames, tailFrames int
}

func NewPool(program Program, sampleRate int) (*Pool, error) {
	p := &Pool{releaseFrames: sampleRate * 30 / 1000, tailFrames: sampleRate * 5 / 1000}
	for i := range p.slots {
		v, err := NewVoice(program, sampleRate)
		if err != nil {
			return nil, err
		}
		p.slots[i].voice = v
	}
	return p, nil
}

// NoteOn starts a complete cohort only after validating every pitch and ID.
// Slides are supported for one pitch only; chords cannot slide or ratchet.
func (p *Pool) NoteOn(notes [4]uint8, count, velocity uint8, slide bool, cohort Cohort) ([4]Handle, error) {
	var handles [4]Handle
	if count < 1 || count > MaxPolyphony || velocity > 127 || cohort.NoteID <= 0 || p.serial > ^uint64(0)-uint64(count) || slide && count != 1 {
		return handles, Error("invalid polyphonic cohort")
	}
	for i := uint8(0); i < count; i++ {
		if notes[i] > 127 {
			return handles, Error("polyphonic pitch is out of range")
		}
		for j := uint8(0); j < i; j++ {
			if notes[i] == notes[j] {
				return handles, Error("polyphonic pitches must be distinct")
			}
		}
	}
	for n := uint8(0); n < count; n++ {
		slot := -1
		if slide {
			for i := range p.slots {
				if p.slots[i].gated && (slot < 0 || p.slots[i].id > p.slots[slot].id) {
					slot = i
				}
			}
		}
		carried := slot >= 0
		if slot < 0 {
			releasing := false
			for i := range p.slots {
				s := &p.slots[i]
				if !s.gated && s.release == 0 && s.tailLeft == 0 {
					slot = i
					break
				}
				if !s.gated {
					level := p.quietness(i)
					old := float64(0)
					if slot >= 0 {
						old = p.quietness(slot)
					}
					if !releasing || level < old {
						slot, releasing = i, true
					}
				} else if !releasing && (slot < 0 || s.id < p.slots[slot].id) {
					slot = i
				}
			}
		}
		s := &p.slots[slot]
		if !carried {
			s.tail, s.tailLeft = s.last, p.tailFrames
			if s.last == 0 {
				s.tailLeft = 0
			}
			s.voice.resetDeterministic()
			s.peak = 0
		}
		s.voice.NoteOn(notes[n], velocity, carried)
		p.serial++
		s.id, s.cohort, s.gated, s.release = p.serial, cohort, true, 0
		handles[n] = Handle{Slot: uint8(slot), ID: s.id}
	}
	return handles, nil
}

func (p *Pool) NoteOff(handle Handle) bool {
	if handle.Slot >= MaxPolyphony || handle.ID == 0 {
		return false
	}
	s := &p.slots[handle.Slot]
	if s.id != handle.ID || !s.gated {
		return false
	}
	s.voice.NoteOff()
	s.gated = false
	s.release = p.releaseFrames
	return true
}

// Release gates only slots belonging to this exact onset and generation.
func (p *Pool) Release(cohort Cohort) bool {
	released := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.cohort == cohort && s.gated {
			released = p.NoteOff(Handle{Slot: uint8(i), ID: s.id}) || released
		}
	}
	return released
}

// SetExpression updates all gated pitches in one sequencer cohort. Stale
// generations and releasing voices retain their own controls.
func (p *Pool) SetExpression(cohort Cohort, params expression.Params) bool {
	updated := false
	for i := range p.slots {
		s := &p.slots[i]
		if s.gated && s.cohort == cohort {
			s.voice.SetExpression(params)
			updated = true
		}
	}
	return updated
}

func (p *Pool) ReleaseAll() {
	for i := range p.slots {
		p.NoteOff(Handle{Slot: uint8(i), ID: p.slots[i].id})
	}
}
func (p *Pool) ActiveVoices() int {
	n := 0
	for i := range p.slots {
		s := &p.slots[i]
		if s.gated || s.release > 0 || s.tailLeft > 0 {
			n++
		}
	}
	return n
}

// Reset retains the monotonic serial so pre-reset handles remain stale. The
// opt-in pool reseeds noise; legacy mono Voice.Reset semantics are unchanged.
func (p *Pool) Reset() {
	for i := range p.slots {
		v := p.slots[i].voice
		v.resetDeterministic()
		p.slots[i] = poolSlot{voice: v}
	}
}

func (p *Pool) Next() float32 {
	var sum float64
	for i := range p.slots {
		s := &p.slots[i]
		var y float32
		if s.gated || s.release > 0 {
			y = s.voice.Next()
			level := y
			if level < 0 {
				level = -level
			}
			if level > s.peak {
				s.peak = level
			}
			if !s.gated {
				y *= float32(s.release) / float32(p.releaseFrames)
				s.release--
				if s.release == 0 {
					s.voice.resetDeterministic()
				}
			}
		}
		if s.tailLeft > 0 {
			y += s.tail * float32(s.tailLeft) / float32(p.tailFrames)
			s.tailLeft--
		}
		s.last = y
		sum += float64(y)
	}
	return float32(sum)
}

func (v *Voice) resetDeterministic() {
	v.Reset()
	for i := 0; i < int(v.program.Len); i++ {
		v.states[i].noise = uint32(i+1)*0x9e3779b9 ^ 0xa5a5a5a5
	}
}

// Peak times remaining release fraction is stable through oscillator zero
// crossings. Include the one bounded declick tail when ranking releases.
func (p *Pool) quietness(slot int) float64 {
	s := &p.slots[slot]
	tail := float64(s.tail)
	if tail < 0 {
		tail = -tail
	}
	return float64(s.peak)*float64(s.release)/float64(p.releaseFrames) + tail*float64(s.tailLeft)/float64(p.tailFrames)
}
