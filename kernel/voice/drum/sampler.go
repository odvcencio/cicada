package drum

import (
	"math"

	"m31labs.dev/cicada/kernel/voice/sample"
)

// SamplerBank is one sample pack voice pool that several kit lanes share. A
// shared pool lets the pack's own choke groups act across the lanes, such as a
// closed hat damping an open hat.
type SamplerBank struct {
	instrument *sample.Instrument
	handles    [32]bankHandle
	next       int
}

type bankHandle struct {
	handle sample.Handle
	note   uint8
	live   bool
}

// NewSamplerBank wraps a pack instrument built for the kit's sample rate.
func NewSamplerBank(instrument *sample.Instrument) *SamplerBank {
	return &SamplerBank{instrument: instrument}
}

func (b *SamplerBank) reset() {
	b.instrument.Reset()
	b.handles = [32]bankHandle{}
	b.next = 0
}

func (b *SamplerBank) noteOn(note, velocity uint8, gainL, gainR float64) error {
	h, err := b.instrument.NoteOnMix(note, velocity, gainL, gainR)
	if err != nil {
		return err
	}
	b.handles[b.next] = bankHandle{handle: h, note: note, live: true}
	b.next = (b.next + 1) % len(b.handles)
	return nil
}

// noteOff releases the notes this lane started. A one-shot zone ignores it.
func (b *SamplerBank) noteOff(note uint8) {
	for i := range b.handles {
		if b.handles[i].live && b.handles[i].note == note {
			b.instrument.NoteOff(b.handles[i].handle)
			b.handles[i].live = false
		}
	}
}

// samplerLane binds one kit lane to a note of a bank.
type samplerLane struct {
	bank         *SamplerBank
	note         uint8
	gainL, gainR float64
}

// SetSampler binds a lane to one note of a shared pack bank before rendering
// begins. levelDB is the lane level; pan is -1..1 and balances the stereo
// recording: centre leaves both channels at unity.
func (k *Kit) SetSampler(lane Lane, bank *SamplerBank, note uint8, levelDB, pan float64) error {
	if lane >= LaneCount {
		return validationError("unsupported drum lane")
	}
	if bank == nil || bank.instrument == nil {
		return validationError("sampler lane needs a pack bank")
	}
	if math.IsNaN(levelDB) || math.IsNaN(pan) || pan < -1 || pan > 1 || levelDB < -60 || levelDB > 6 {
		return validationError("sampler lane level or pan is out of range")
	}
	level := math.Pow(10, levelDB/20)
	v := &k.lanes[lane]
	v.enabled = true
	v.custom, v.customOld, v.modeled = nil, nil, nil
	v.customActive, v.customChoke, v.fadeRemaining = false, 0, 0
	v.current, v.old = state{}, state{}
	v.sampler = &samplerLane{bank: bank, note: note, gainL: level * math.Min(1, 1-pan), gainR: level * math.Min(1, 1+pan)}
	k.addBank(bank)
	return nil
}

func (k *Kit) addBank(bank *SamplerBank) {
	for _, existing := range k.banks {
		if existing == bank {
			return
		}
	}
	k.banks = append(k.banks, bank)
}
