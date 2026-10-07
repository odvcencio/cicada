package engine

import "m31labs.dev/cicada/kernel/voice/sample"

// PackFactory contains only verified immutable PCM and a bounded instrument
// map. Native, offline and worklet engines each construct their own owner.
type PackFactory struct {
	Zones  []sample.Zone
	Config sample.InstrumentConfig
}

func (p *PackFactory) VoiceCount() int { return p.Config.Voices }
func (p *PackFactory) NewStereoVoice(rate int) (StereoVoice, error) {
	inst, err := sample.NewInstrument(rate, p.Zones, p.Config)
	if err != nil {
		return nil, err
	}
	return &packVoice{instrument: inst}, nil
}

type packVoice struct {
	instrument *sample.Instrument
	handles    [32]sample.Handle
	next       int
}

func (v *packVoice) NextStereo() (float32, float32) { return v.instrument.NextStereo() }
func (v *packVoice) NoteOn(note, velocity uint8) error {
	h, err := v.instrument.NoteOn(note, velocity)
	if err == nil {
		v.handles[v.next] = h
		v.next = (v.next + 1) % len(v.handles)
	}
	return err
}
func (v *packVoice) NoteOff() {
	for i := range v.handles {
		v.instrument.NoteOff(v.handles[i])
	}
}
func (v *packVoice) Reset()                              { v.instrument.Reset(); v.handles = [32]sample.Handle{}; v.next = 0 }
func (v *packVoice) SelectSlot(uint8, int64, bool) error { return nil }
func (v *packVoice) Play() error                         { return nil }
