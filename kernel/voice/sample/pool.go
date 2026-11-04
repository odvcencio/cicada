package sample

import "m31labs.dev/cicada/kernel/expression"

const MaxVoices = 64

// Handle identifies a note, including repeated pitches and reused slots.
// IDs are monotonic; stale handles cannot stop the replacement note.
type Handle struct {
	Slot int
	ID   uint64
}

// Cohort names a sequenced or live note independently of its pitch and slot.
type Cohort struct {
	NoteID     int64
	Generation uint64
}

// Pool owns a fixed set of slots and sums them in ascending index order.
// NoteOn chooses the lowest free slot, then the quietest releasing voice
// (gain times release fraction, including a steal tail), otherwise the oldest
// note. Every tie uses the lowest slot index. No extra voice is created on steal.
type Pool struct {
	voices  [MaxVoices]Voice
	ids     [MaxVoices]uint64
	cohorts [MaxVoices]Cohort
	groups  [MaxVoices]expression.Group
	gated   [MaxVoices]bool
	serial  uint64
	limit   int
}

func NewPool(sampleRate, maximum int, region Region) (*Pool, error) {
	if maximum < 1 || maximum > MaxVoices {
		return nil, validationError("sample pool maximum must be between 1 and 64")
	}
	if err := validateRate(sampleRate); err != nil {
		return nil, err
	}
	if err := region.Validate(); err != nil {
		return nil, err
	}
	prepareBanks()
	p := &Pool{limit: maximum}
	for i := 0; i < maximum; i++ {
		p.voices[i].configure(sampleRate, region)
	}
	return p, nil
}

func (p *Pool) SetParams(params Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	for i := 0; i < p.limit; i++ {
		_ = p.voices[i].SetParams(params)
	}
	return nil
}

// SetGainTarget smooths the same live gain target independently in every slot.
func (p *Pool) SetGainTarget(gain, alpha float64) error {
	for i := 0; i < p.limit; i++ {
		if err := p.voices[i].SetGainTarget(gain, alpha); err != nil {
			return err
		}
	}
	return nil
}

// Playable reports whether a note's unbent pitch is within the resampler range.
func (p *Pool) Playable(note uint8) bool {
	_, err := p.voices[0].playbackRatio(note)
	return err == nil
}

func (p *Pool) NoteOn(note, velocity uint8) (Handle, error) {
	if _, err := p.voices[0].playbackRatio(note); err != nil {
		return Handle{}, err
	}
	if velocity > 127 || p.serial == ^uint64(0) {
		return Handle{}, validationError("sample velocity or note ID is out of range")
	}
	slot, releasing := -1, false
	var quietest float64
	for i := 0; i < p.limit; i++ {
		v := &p.voices[i]
		if !v.Active() {
			slot = i
			break
		}
		if v.Releasing() {
			level := v.quietness()
			if !releasing || level < quietest {
				slot, quietest, releasing = i, level, true
			}
		} else if !releasing && (slot == -1 || p.ids[i] < p.ids[slot]) {
			slot = i
		}
	}
	_ = p.voices[slot].NoteOn(note, velocity)
	p.serial++
	p.ids[slot] = p.serial
	if sampleExpressionEnabled {
		p.cohorts[slot], p.groups[slot], p.gated[slot] = Cohort{}, expression.Group{}, velocity != 0
	}
	return Handle{Slot: slot, ID: p.serial}, nil
}

func (p *Pool) NoteOnCohort(note, velocity uint8, cohort Cohort) (Handle, error) {
	h, err := p.NoteOn(note, velocity)
	if err == nil {
		p.cohorts[h.Slot] = cohort
	}
	return h, err
}

func (p *Pool) NoteOff(handle Handle) bool {
	if handle.Slot < 0 || handle.Slot >= p.limit || handle.ID == 0 || p.ids[handle.Slot] != handle.ID {
		return false
	}
	p.voices[handle.Slot].NoteOff()
	if sampleExpressionEnabled {
		p.gated[handle.Slot] = false
	}
	return true
}

func (p *Pool) ReleaseCohort(cohort Cohort) bool {
	released := false
	for i := 0; i < p.limit; i++ {
		if p.gated[i] && p.cohorts[i] == cohort {
			p.voices[i].NoteOff()
			p.gated[i] = false
			released = true
		}
	}
	return released
}

func (p *Pool) ReleaseGeneration(generation uint64) {
	for i := 0; i < p.limit; i++ {
		if p.gated[i] && p.cohorts[i].Generation == generation {
			p.voices[i].NoteOff()
			p.gated[i] = false
		}
	}
}

func (p *Pool) SetExpression(handle Handle, params expression.Params) error {
	if handle.Slot < 0 || handle.Slot >= p.limit || handle.ID == 0 || p.ids[handle.Slot] != handle.ID || !p.gated[handle.Slot] {
		return nil
	}
	return p.voices[handle.Slot].SetExpression(params)
}

func (p *Pool) SetControls(handle Handle, pitch, pressure, timbre float32) error {
	if handle.Slot < 0 || handle.Slot >= p.limit || handle.ID == 0 || p.ids[handle.Slot] != handle.ID || !p.gated[handle.Slot] {
		return nil
	}
	return p.voices[handle.Slot].noteExpression(pitch, pressure, timbre)
}

func (p *Pool) SetCohortExpression(cohort Cohort, params expression.Params) error {
	for i := 0; i < p.limit; i++ {
		if p.gated[i] && p.cohorts[i] == cohort {
			if err := p.voices[i].SetExpression(params); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pool) SetCohortControls(cohort Cohort, pitch, pressure, timbre float32) error {
	for i := 0; i < p.limit; i++ {
		if p.gated[i] && p.cohorts[i] == cohort {
			if err := p.voices[i].noteExpression(pitch, pressure, timbre); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pool) BindGroup(cohort Cohort, group expression.Group, controls expression.Controls) error {
	if group.ID == 0 {
		return nil
	}
	for i := 0; i < p.limit; i++ {
		if p.gated[i] && p.cohorts[i] == cohort {
			if err := p.voices[i].setGroupControls(controls); err != nil {
				return err
			}
			p.groups[i] = group
		}
	}
	return nil
}

func (p *Pool) SetGroupControls(group expression.Group, controls expression.Controls) error {
	if group.ID == 0 {
		return nil
	}
	for i := 0; i < p.limit; i++ {
		if p.voices[i].Active() && p.groups[i] == group {
			if err := p.voices[i].setGroupControls(controls); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Pool) GroupActive(group expression.Group) bool {
	if group.ID == 0 {
		return false
	}
	for i := 0; i < p.limit; i++ {
		if p.voices[i].Active() && p.groups[i] == group {
			return true
		}
	}
	return false
}

func (p *Pool) DetachGroups() { clear(p.groups[:]) }

func (p *Pool) ActiveVoices() int {
	count := 0
	for i := 0; i < p.limit; i++ {
		if p.voices[i].Active() {
			count++
		}
	}
	return count
}

// NextStereo sums the fixed pool without allocating.
func (p *Pool) NextStereo() (float32, float32) {
	var left, right float64
	for i := 0; i < p.limit; i++ {
		l, r := p.voices[i].NextStereo()
		left += float64(l)
		right += float64(r)
	}
	return float32(left), float32(right)
}

func (p *Pool) NoteOffAll() {
	for i := 0; i < p.limit; i++ {
		p.voices[i].NoteOff()
		if sampleExpressionEnabled {
			p.gated[i] = false
		}
	}
}

func (p *Pool) Reset() {
	for i := 0; i < p.limit; i++ {
		p.voices[i].Reset()
		p.ids[i] = 0
		if sampleExpressionEnabled {
			p.cohorts[i], p.groups[i], p.gated[i] = Cohort{}, expression.Group{}, false
		}
	}
	// Keep serial monotonic so handles from before Reset remain stale.
}

// Render overwrites outputs. Each frame sums float64 voice outputs in slot
// order, then rounds once to float32, independent of the requested block size.
func (p *Pool) Render(left, right []float32) {
	if len(left) != len(right) {
		panic("sample pool output channel lengths differ")
	}
	for frame := range left {
		var l, r float64
		for slot := 0; slot < p.limit; slot++ {
			vl, vr := p.voices[slot].NextStereo()
			l += float64(vl)
			r += float64(vr)
		}
		left[frame], right[frame] = float32(l), float32(r)
	}
}
