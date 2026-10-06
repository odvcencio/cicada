// Package fixture supplies identical FM note events to Go and TinyGo.
package fixture

import "m31labs.dev/cicada/kernel/voice/fm"

const Frames = 16384
const Cases = 16

func Render(index, blockSize int, output []float32) error {
	if index < 0 || index >= Cases || blockSize < 1 || Frames%blockSize != 0 || len(output) < Frames*2 {
		return fm.Error("invalid FM fixture arguments")
	}
	rate, name, voiceLimit := 48000, "fm_ep", fm.MaxVoices
	if index < 12 {
		rate = [...]int{44100, 48000, 96000, 192000}[index%4]
		name = [...]string{"fm_ep", "bell_keys", "fm_bass"}[index/4]
	} else {
		voiceLimit = [...]int{1, 2, 4, 8}[index-12]
	}
	p, err := fm.Patch(name)
	if err != nil {
		return err
	}
	f, err := fm.New(rate, p)
	if err != nil {
		return err
	}
	if err := f.SetVoiceLimit(voiceLimit); err != nil {
		return err
	}
	for start := 0; start < Frames; start += blockSize {
		for frame := start; frame < start+blockSize; frame++ {
			switch frame {
			case 0:
				_ = f.NoteOn(21, 127)
				_ = f.NoteOn(60, 75)
				_ = f.NoteOn(108, 100)
			case 1024:
				_ = f.SetSustain(1)
				f.NoteOff(60)
			case 2048:
				_ = f.NoteOn(60, 110)
			case 4096:
				for i := range 10 {
					_ = f.NoteOn(uint8(36+i*5), uint8(40+i*8))
				}
			case 8192:
				f.AllNotesOff()
				_ = f.SetSustain(.5)
			case 12288:
				_ = f.SetSustain(0)
			case 14336:
				f.Reset()
				_ = f.NoteOn(64, 90)
			}
			output[frame], output[Frames+frame] = f.NextStereo()
		}
	}
	return nil
}
