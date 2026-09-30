package sample

const MaxVoices = 64

// Handle identifies a note, including repeated pitches and reused slots.
// IDs are monotonic; stale handles cannot stop the replacement note.
type Handle struct {
	Slot int
	ID   uint64
}

// Pool owns a fixed set of slots and sums them in ascending index order.
// NoteOn chooses the lowest free slot, then the quietest releasing voice
// (gain times release fraction, including a steal tail), otherwise the oldest
// note. Every tie uses the lowest slot index. No extra voice is created on steal.
type Pool struct {
	voices [MaxVoices]Voice
	ids    [MaxVoices]uint64
	serial uint64
	limit  int
}

func NewPool(sampleRate, maximum int, region Region) (*Pool, error) {
	if maximum < 1 || maximum > MaxVoices {
		return nil, Error("sample pool maximum must be between 1 and 64")
	}
	if err := validateRate(sampleRate); err != nil {
		return nil, err
	}
	if err := region.Validate(); err != nil {
		return nil, err
	}
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

func (p *Pool) NoteOn(note, velocity uint8) (Handle, error) {
	if _, err := p.voices[0].playbackRatio(note); err != nil {
		return Handle{}, err
	}
	if velocity > 127 || p.serial == ^uint64(0) {
		return Handle{}, Error("sample velocity or note ID is out of range")
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
	return Handle{Slot: slot, ID: p.serial}, nil
}

func (p *Pool) NoteOff(handle Handle) bool {
	if handle.Slot < 0 || handle.Slot >= p.limit || handle.ID == 0 || p.ids[handle.Slot] != handle.ID {
		return false
	}
	p.voices[handle.Slot].NoteOff()
	return true
}

func (p *Pool) ActiveVoices() int {
	count := 0
	for i := 0; i < p.limit; i++ {
		if p.voices[i].Active() {
			count++
		}
	}
	return count
}

func (p *Pool) Reset() {
	for i := 0; i < p.limit; i++ {
		p.voices[i].Reset()
		p.ids[i] = 0
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
