// Package fixture supplies identical native and TinyGo sample-voice inputs.
package fixture

import "m31labs.dev/cicada/kernel/voice/sample"

const Frames = 4096
const Cases = 14

// Render includes intro, loop wraps, repeated pitches, smoothing, release and
// a full-pool steal. Integer-generated PCM avoids platform-specific tone math.
func Render(index, blockSize int, output []float32) error {
	if index == 13 {
		return renderRecordedInstrument(blockSize, output)
	}
	if index >= 9 {
		return renderCrossfadedLoop(index, blockSize, output)
	}
	if index >= 6 {
		return renderInstrument(index, blockSize, output)
	}
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

// Larger crossfaded loops exercise contiguous and mapped FIR windows in both
// channels, before and after wrapping, at 48 and 96 kHz output rates.
func renderCrossfadedLoop(index, blockSize int, output []float32) error {
	var left, right [2049]float32
	seed := uint32(0x71b927cd)
	for i := range left {
		seed = seed*1664525 + 1013904223
		left[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
		seed = seed*1664525 + 1013904223
		right[i] = float32(int32(seed>>8)-(1<<23)) / (1 << 24)
	}
	r := sample.Region{Left: left[:], SampleRate: 48000, RootKey: 60, Start: 7, End: 2047, Loop: true, LoopStart: 303, LoopEnd: 1927, Crossfade: 256}
	rate, note := 48000, uint8(61)
	switch index {
	case 9:
		r.Right = right[:]
	case 10:
		rate, note = 96000, 73
	case 11:
		r.Right, r.Crossfade, note = right[:], 800, 84
	case 12:
		rate, r.Crossfade = 44100, 640
	}
	p, err := sample.NewPool(rate, 3, r)
	if err != nil {
		return err
	}
	first, err := p.NoteOn(note, 127)
	if err != nil {
		return err
	}
	if _, err = p.NoteOn(note+4, 103); err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		if frame == 1024 {
			p.NoteOff(first)
			if err = p.SetGainTarget(.7, .013); err != nil {
				return err
			}
		}
		if frame == 2048 {
			for j := 0; j < 3; j++ {
				if _, err = p.NoteOn(note+uint8(j), uint8(81+j)); err != nil {
					return err
				}
			}
		}
		p.Render(output[frame:frame+blockSize], output[Frames+frame:Frames+frame+blockSize])
	}
	return nil
}

// Mapped fixtures include velocity blending, take cycles, pedal, releases,
// seeded humanization, legato and stealing at all supported output rates.
func renderInstrument(index, blockSize int, output []float32) error {
	var pcm [513]float32
	for i := range pcm {
		pcm[i] = float32((i*17)%101-50) / 100
	}
	rate := 48000
	if index == 7 {
		rate = 44100
	}
	if index == 8 {
		rate = 96000
	}
	var zones []sample.Zone
	for layer := 0; layer < 2; layer++ {
		for rr := 0; rr < 2; rr++ {
			for trigger := 0; trigger < 2; trigger++ {
				r := sample.Region{Left: pcm[:], SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: trigger == 0, LoopStart: 31, LoopEnd: 501}
				if trigger == 0 {
					r.Crossfade = 101
				}
				zones = append(zones, sample.Zone{Region: r, KeyLow: 48, KeyHigh: 72, VelocityLow: 1, VelocityHigh: 127, Layer: uint8(32 + layer*64), Group: 0, Position: uint8(rr), Count: 2, Release: trigger == 1, Gain: .2 + float64(rr)*.1})
			}
		}
	}
	c := sample.DefaultInstrumentConfig()
	c.Voices = 3
	c.Amp = sample.Envelope{Attack: 3, Decay: 7, Sustain: .7, Release: 20}
	c.Filter = sample.Envelope{Attack: 4, Decay: 8, Sustain: .5, Release: 12}
	c.Cutoff = 400
	c.FilterDepth = 5000
	c.Humanize = sample.Humanize{Seed: 4242, DelayMS: 1, Velocity: 2, Cents: 1}
	p, err := sample.NewInstrument(rate, zones, c)
	if err != nil {
		return err
	}
	h, err := p.NoteOn(60, 65)
	if err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		if frame == 512 {
			if err = p.Legato(h, 62, 2); err != nil {
				return err
			}
		}
		if frame == 1024 {
			p.Sustain(true)
			p.NoteOff(h)
		}
		if frame == 1536 {
			p.Sustain(false)
		}
		if frame == 2048 {
			for j := 0; j < 5; j++ {
				if _, err = p.NoteOn(uint8(60+j), uint8(64+j)); err != nil {
					return err
				}
			}
		}
		p.Render(output[frame:frame+blockSize], output[Frames+frame:Frames+frame+blockSize])
	}
	return nil
}

// renderRecordedInstrument plays a pack whose zones share one gain, so the
// recorded-dynamics law runs: two attack layers whose velocity ranges split at
// 64, one release layer, velocities below, between and above the centres.
func renderRecordedInstrument(blockSize int, output []float32) error {
	var soft, loud [701]float32
	for i := range soft {
		soft[i] = float32((i*13)%97-48) / 400
		loud[i] = float32((i*29)%89-44) / 100
	}
	var zones []sample.Zone
	for _, layer := range []struct {
		pcm             []float32
		centre, low, hi uint8
	}{{soft[:], 32, 1, 63}, {loud[:], 96, 64, 127}} {
		r := sample.Region{Left: layer.pcm, SampleRate: 48000, RootKey: 60, End: len(layer.pcm), Loop: true, LoopStart: 41, LoopEnd: 677, Crossfade: 37}
		zones = append(zones, sample.Zone{Region: r, KeyLow: 48, KeyHigh: 72, VelocityLow: layer.low, VelocityHigh: layer.hi, Layer: layer.centre, Count: 1, Gain: .25})
	}
	release := sample.Region{Left: soft[:300], SampleRate: 48000, RootKey: 60, End: 300}
	zones = append(zones, sample.Zone{Region: release, KeyLow: 48, KeyHigh: 72, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Count: 1, Release: true, Gain: .25})
	c := sample.DefaultInstrumentConfig()
	c.Voices = 4
	c.Amp = sample.Envelope{Attack: 2, Decay: 5, Sustain: .8, Release: 15}
	p, err := sample.NewInstrument(48000, zones, c)
	if err != nil {
		return err
	}
	var held [4]sample.Handle
	for frame := 0; frame < Frames; frame += blockSize {
		for i, at := range [4]int{0, 768, 1536, 2304} {
			if frame == at {
				if held[i], err = p.NoteOn(uint8(60+i), [4]uint8{9, 47, 70, 120}[i]); err != nil {
					return err
				}
			}
			if frame == at+512 {
				p.NoteOff(held[i])
			}
		}
		p.Render(output[frame:frame+blockSize], output[Frames+frame:Frames+frame+blockSize])
	}
	return nil
}
