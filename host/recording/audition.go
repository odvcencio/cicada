package recording

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/voice/sample"
)

// Audition uses the same native sample voice as score playback. Selection is
// explicit so independent clients can reproduce the same round-robin cycle.
func (p *Pack) Audition(note, velocity, cycle int) ([]byte, int, error) {
	if note < 0 || note > 127 || velocity < 1 || velocity > 127 || cycle < 0 {
		return nil, 0, fmt.Errorf("choose a MIDI note and velocity and a nonnegative take cycle")
	}
	for _, z := range p.Manifest.Zones {
		if note < z.KeyLow || note > z.KeyHigh || velocity < z.VelocityLow || velocity > z.VelocityHigh || z.Position != cycle%z.Count {
			continue
		}
		index := -1
		for i, a := range p.Manifest.Assets {
			if a.ID == z.Asset {
				index = i
				break
			}
		}
		if index < 0 || index >= len(p.Hits) {
			return nil, 0, fmt.Errorf("zone references missing hit")
		}
		h := p.Hits[index]
		voice, err := sample.New(48000, sample.Region{Left: h.PCM, SampleRate: h.Rate, RootKey: uint8(z.Root), End: len(h.PCM)})
		if err != nil {
			return nil, 0, err
		}
		if err := voice.SetParams(sample.Params{Gain: z.Gain, FineTuneCents: z.TuneCents}); err != nil {
			return nil, 0, err
		}
		if err := voice.NoteOn(uint8(note), uint8(velocity)); err != nil {
			return nil, 0, err
		}
		frames := int(math.Ceil(float64(len(h.PCM))/voice.Ratio())) + 96
		if frames > MaxFrames {
			return nil, 0, fmt.Errorf("audition exceeds frame limit")
		}
		pcm := make([]float32, frames)
		for i := range pcm {
			left, right := voice.NextStereo()
			pcm[i] = (left + right) * .5
		}
		return EncodeWAV(pcm, 48000), index, nil
	}
	return nil, 0, fmt.Errorf("note has no recorded zone; choose a note near a recorded root")
}
