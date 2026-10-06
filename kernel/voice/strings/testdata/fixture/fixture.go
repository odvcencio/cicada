// Package fixture supplies identical events to Go and TinyGo.
package fixture

import "m31labs.dev/cicada/kernel/voice/strings"

const Frames = 16384
const Cases = 4

func Render(index, blockSize int, output []float32) error {
	rates := [...]int{44100, 48000, 96000, 192000}
	i, err := strings.New(rates[index], strings.DefaultParams())
	if err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		switch frame {
		case 0:
			_ = i.NoteOn(21, 127)
			_ = i.NoteOn(60, 75)
			_ = i.NoteOn(108, 100)
		case 1024:
			_ = i.SetSustain(1)
			i.NoteOff(60)
		case 2048:
			_ = i.NoteOn(60, 110)
		case 4096:
			_ = i.SetVoiceLimit(index%4 + 1)
			for n := 0; n < 10; n++ {
				_ = i.NoteOn(uint8(36+n*5), uint8(40+n*8))
			}
		case 8192:
			i.AllNotesOff()
			_ = i.SetVoiceLimit(8)
		case 12288:
			i.Reset()
			_ = i.NoteOn(64, 82)
		}
		for n := frame; n < frame+blockSize; n++ {
			output[n], output[Frames+n] = i.NextStereo()
		}
	}
	return nil
}
