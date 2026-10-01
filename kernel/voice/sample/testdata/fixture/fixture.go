// Package fixture supplies identical native and TinyGo sample-voice inputs.
package fixture

import "m31labs.dev/cicada/kernel/voice/sample"

const Frames = 4096
const Cases = 6

// Render includes intro, loop wraps, repeated pitches, smoothing, release and
// a full-pool steal. Integer-generated PCM avoids platform-specific tone math.
func Render(index, blockSize int, output []float32) error {
	var left, right [257]float32
	seed := uint32(0x13579bdf)
	for i := range left {
		seed = seed*1664525 + 1013904223
		left[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
		seed = seed*1664525 + 1013904223
		right[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
	}
	r := sample.Region{Left: left[:], Right: right[:], SampleRate: 48000, RootKey: 60, Start: 3, End: 257, Loop: true, LoopStart: 17, LoopEnd: 239}
	rate, note, tune := 48000, uint8(60), 0.0
	switch index {
	case 1:
		note, tune = 67, 11.25
	case 2:
		note = 84
	case 3:
		r.SampleRate = 44100
	case 4:
		rate = 44100
	case 5:
		note = 36
	}
	p, err := sample.NewPool(rate, 3, r)
	if err != nil {
		return err
	}
	if err := p.SetParams(sample.Params{Gain: 1, FineTuneCents: tune}); err != nil {
		return err
	}
	first, err := p.NoteOn(note, 127)
	if err != nil {
		return err
	}
	if _, err := p.NoteOn(note, 93); err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		if frame == 1024 {
			p.NoteOff(first)
			if err := p.SetGainTarget(.7, .013); err != nil {
				return err
			}
		}
		if frame == 2048 {
			for i := 0; i < 3; i++ {
				if _, err := p.NoteOn(note, uint8(81+i)); err != nil {
					return err
				}
			}
		}
		p.Render(output[frame:frame+blockSize], output[Frames+frame:Frames+frame+blockSize])
	}
	return nil
}
