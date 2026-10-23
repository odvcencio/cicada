package graph

import (
	"m31labs.dev/cicada/kernel/expression"
	"math"
)

const PolyVoices = 8

// Poly owns a fixed eight-voice pool. Construction prepares every graph and
// oscillator table; notes, releases, stealing and rendering allocate nothing.
type Poly struct {
	slots      [PolyVoices]polySlot
	serial     uint64
	fadeFrames int
	tail       float32
	tailFrames int
	stolen     [128]uint32
}
type polySlot struct {
	voice                      *Voice
	age                        uint64
	identity                   uint16
	group                      expression.Group
	note                       uint8
	active, held               bool
	last                       float32
	quietFrames, releaseFrames int
}

//go:noinline
func NewPoly(program Program, sampleRate int) (*Poly, error) {
	return NewPolyFromProgram(&program, sampleRate)
}

// NewPolyFromProgram prepares the fixed voice pool from an immutable program.
func NewPolyFromProgram(program *Program, sampleRate int) (*Poly, error) {
	p := new(Poly)
	p.fadeFrames = sampleRate / 500
	for i := range p.slots {
		v, err := NewVoiceFromProgram(program, sampleRate)
		if err != nil {
			return nil, err
		}
		p.slots[i].voice = v
	}
	return p, nil
}
func (p *Poly) NoteOn(note, velocity uint8, slide bool) {
	p.noteOn(note, velocity, slide)
}
func (p *Poly) noteOn(note, velocity uint8, slide bool) int {
	if note >= 128 {
		return -1
	}
	index := -1
	if slide { // Legato addresses the most recently held note.
		var newest uint64
		for i := range p.slots {
			s := &p.slots[i]
			if s.held && s.identity == 0 && s.age >= newest {
				index, newest = i, s.age
			}
		}
		if index >= 0 {
			s := &p.slots[index]
			s.note = note
			s.group = expression.Group{}
			s.voice.NoteOn(note, velocity, true)
			return index
		}
	}
	for i := range p.slots {
		if !p.slots[i].active {
			index = i
			break
		}
	}
	if index < 0 {
		quietest := float32(math.MaxFloat32)
		for i := range p.slots {
			s := &p.slots[i]
			if s.held {
				continue
			}
			level, _ := s.voice.envelopeLevel()
			if level < quietest {
				quietest, index = level, i
			}
		}
	}
	if index < 0 {
		index = 0
		for i := 1; i < PolyVoices; i++ {
			if p.slots[i].age < p.slots[index].age {
				index = i
			}
		}
	}
	s := &p.slots[index]
	if s.active {
		if s.held && s.identity == 0 && p.stolen[s.note] < math.MaxUint32 {
			p.stolen[s.note]++
		}
		if p.tailFrames > 0 {
			p.tail *= float32(p.tailFrames) / float32(p.fadeFrames)
		}
		p.tail += s.last
		p.tailFrames = p.fadeFrames
	}
	s.voice.Reset()
	s.voice.NoteOn(note, velocity, false)
	p.serial++
	s.voice.spreadPhase(uint32(p.serial) * 0x9e3779b9)
	s.age, s.note, s.active, s.held = p.serial, note, true, true
	s.identity = 0
	s.group = expression.Group{}
	s.last, s.quietFrames, s.releaseFrames = 0, 0, 0
	return index
}

// NoteOffNote releases the oldest held instance of a repeated pitch, matching
// ordered MIDI note-on/off pairs. Release tails keep their independent voices.
func (p *Poly) NoteOffNote(note uint8) {
	if note >= 128 {
		return
	}
	// A stolen held note still owns its eventual MIDI note-off. Consume that
	// pair before releasing a later instance of the same pitch.
	if p.stolen[note] > 0 {
		p.stolen[note]--
		return
	}
	index := -1
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == 0 && s.note == note && (index < 0 || s.age < p.slots[index].age) {
			index = i
		}
	}
	if index >= 0 {
		s := &p.slots[index]
		s.held = false
		s.voice.NoteOff()
	}
}

func (p *Poly) NoteOnIdentity(note, velocity uint8, id uint16) bool {
	if id == 0 || id == 65535 || note > 127 || velocity > 127 {
		return false
	}
	p.NoteOffIdentity(id)
	slot := p.noteOn(note, velocity, false)
	p.slots[slot].identity = id
	return true
}

// NoteOffIdentity never releases legacy or sequenced notes, including when a
// stolen voice has already been replaced by another note of the same pitch.
func (p *Poly) NoteOffIdentity(id uint16) {
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity != 0 && (s.identity == id || id == 65535) {
			s.held = false
			s.voice.NoteOff()
		}
	}
}

func (p *Poly) NoteExpressionIdentity(id uint16, pitch, pressure, timbre float32) {
	if id == 0 || id == 65535 {
		return
	}
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == id {
			s.voice.NoteExpression(pitch, pressure, timbre)
		}
	}
}
func (p *Poly) NoteOff() {
	clear(p.stolen[:])
	for i := range p.slots {
		s := &p.slots[i]
		if s.held {
			s.held = false
			s.voice.NoteOff()
		}
	}
}

func (p *Poly) ReleaseSequenced() {
	clear(p.stolen[:])
	for i := range p.slots {
		s := &p.slots[i]
		if s.held && s.identity == 0 {
			s.held = false
			s.voice.NoteOff()
		}
	}
}
func (p *Poly) Reset() {
	for i := range p.slots {
		v := p.slots[i].voice
		v.Reset()
		p.slots[i] = polySlot{voice: v}
	}
	p.serial, p.tail, p.tailFrames = 0, 0, 0
	clear(p.stolen[:])
}
func (p *Poly) NextStereo() (float32, float32) {
	var out float32
	for i := range p.slots {
		s := &p.slots[i]
		if !s.active {
			continue
		}
		x := s.voice.Next()
		if !s.held {
			level, hasEnvelope := s.voice.envelopeLevel()
			s.releaseFrames++
			if !hasEnvelope {
				x *= float32(max(0, p.fadeFrames-s.releaseFrames)) / float32(p.fadeFrames)
				level = 0
			}
			if level < 1e-7 && math.Abs(float64(x)) < 1e-7 {
				s.quietFrames++
			} else {
				s.quietFrames = 0
			}
			if s.quietFrames >= 64 {
				s.active = false
				s.voice.Reset()
				x = 0
			}
		}
		s.last = x
		out += x
	}
	if p.tailFrames > 0 {
		out += p.tail * float32(p.tailFrames) / float32(p.fadeFrames)
		p.tailFrames--
		if p.tailFrames == 0 {
			p.tail = 0
		}
	}
	// Fixed patch gain, independent of held-note count: no chord-volume pumping.
	return out, out
}

// Idle reports a completely inactive finite voice pool, including its bounded
// stolen-note tail. Unlike a monophonic graph there are no free-running graph
// states while idle: slots are already skipped by NextStereo.
func (p *Poly) Idle() bool {
	if p.tailFrames > 0 {
		return false
	}
	for i := range p.slots {
		if p.slots[i].active {
			return false
		}
	}
	return true
}
