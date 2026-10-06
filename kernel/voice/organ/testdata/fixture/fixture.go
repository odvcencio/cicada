// Package fixture supplies the same organ events to Go and TinyGo.
package fixture

import "m31labs.dev/cicada/kernel/voice/organ"

const Frames = 16384
const Cases = 4

func Render(index, blockSize int, output []float32) error {
	rate := [...]int{44100, 48000, 96000, 192000}[index]
	p, err := organ.New(rate, organ.DefaultParams())
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
			_ = p.SetVoiceLimit(4)
			_ = p.NoteOn(60, 110)
			p.SetRotaryFast(true)
			_ = p.SetDrawbars([9]uint8{8, 5, 8, 6, 2, 4, 1, 2, 3})
		case 4096:
			for i := 0; i < 10; i++ {
				_ = p.NoteOn(uint8(36+i*5), uint8(40+i*8))
			}
		case 8192:
			p.AllNotesOff()
			_ = p.SetVoiceLimit(1)
			_ = p.SetSustain(.5)
		case 12288:
			_ = p.SetVoiceLimit(8)
			_ = p.SetSustain(0)
			p.SetRotaryFast(false)
		}
		for i := frame; i < frame+blockSize; i++ {
			output[i], output[Frames+i] = p.NextStereo()
		}
	}
	return nil
}
