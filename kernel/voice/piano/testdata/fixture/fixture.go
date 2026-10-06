// Package fixture supplies identical piano events to Go and TinyGo.
package fixture

import "m31labs.dev/cicada/kernel/voice/piano"

const Frames = 16384
const Cases = 3

func Render(index, blockSize int, output []float32) error {
	rate := 48000
	if index == 1 {
		rate = 44100
	} else if index == 2 {
		rate = 96000
	}
	p, err := piano.New(rate)
	if err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		switch frame {
		case 0:
			_ = p.NoteOn(21, 127)
			_ = p.NoteOn(60, 75)
			_ = p.NoteOn(108, 100)
		case 1024:
			_ = p.SetSustain(1)
			p.NoteOff(60)
		case 2048:
			_ = p.NoteOn(60, 110)
		case 4096:
			for i := 0; i < 10; i++ {
				_ = p.NoteOn(uint8(36+i*5), uint8(40+i*8))
			}
		case 8192:
			p.AllNotesOff()
			_ = p.SetSustain(.5)
		case 12288:
			_ = p.SetSustain(0)
		}
		for i := frame; i < frame+blockSize; i++ {
			output[i], output[Frames+i] = p.NextStereo()
		}
	}
	return nil
}
