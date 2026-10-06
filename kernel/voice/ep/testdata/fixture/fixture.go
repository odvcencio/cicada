package fixture

import "m31labs.dev/cicada/kernel/voice/ep"

const Frames = 16384
const Cases = 8

func Render(index, blockSize int, out []float32) error {
	rate := []int{44100, 48000, 96000, 192000}[index%4]
	patchName := "tine_ep"
	if index >= 4 {
		patchName = "reed_ep"
	}
	p, _ := ep.Patch(patchName)
	i, err := ep.New(rate, p)
	if err != nil {
		return err
	}
	for frame := 0; frame < Frames; frame += blockSize {
		switch frame {
		case 0:
			_ = i.NoteOn(21, 127)
			_ = i.NoteOn(60, 45)
			_ = i.NoteOn(108, 100)
		case 1024:
			_ = i.SetSustain(1)
			i.NoteOff(60)
		case 2048:
			_ = i.NoteOn(60, 110)
		case 4096:
			for n := 0; n < 10; n++ {
				_ = i.NoteOn(uint8(36+n*5), uint8(40+n*8))
			}
		case 8192:
			i.AllNotesOff()
		case 12288:
			_ = i.SetSustain(0)
		}
		for j := frame; j < frame+blockSize; j++ {
			out[j], out[Frames+j] = i.NextStereo()
		}
	}
	return nil
}
