package fixture

import "m31labs.dev/cicada/kernel/voice/ep"

const Frames = 16384

var patches = [...]string{"tine_ep", "tine_bell", "tine_bark", "tine_tremolo", "reed_ep", "reed_tremolo"}

const Cases = 4 * len(patches)

func Render(index, blockSize int, out []float32) error {
	rate := []int{44100, 48000, 96000, 192000}[index%4]
	patchName := patches[index/4]
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

// GoldenHashes pins planar float32 PCM, little-endian FNV-1a, for every
// patch at 44.1, 48, 96 and 192 kHz in patches order.
var GoldenHashes = [Cases]uint64{
	0x4d6ea147ad116a0d,
	0x2ed053d7b2dabe45,
	0xd61bb12786b2ad2d,
	0x335474a1b3e64325,
	0xeaedac4b05268825,
	0x5e5d48d9bc4d9b05,
	0xccf9ced4dc0179d1,
	0x62f00f95ddba939d,
	0x60ecf604c42d2b89,
	0x19445540c829c625,
	0x07f794b303a9bc01,
	0xa37f932dfc545f71,
	0x4022965947a8c085,
	0x60aa6ae188c7a6da,
	0x69215616b67334a8,
	0x7f385be6142705ac,
	0xdc0a40e75f423849,
	0xf5234e6d4dcfcf9d,
	0x2d7d10ffb0231fa1,
	0xfc2ed81d50f0ef4d,
	0x0b7c5e3077c06f15,
	0xb07f7965a74e401d,
	0x6cb5c6f75dca50f1,
	0xdafced7892694445,
}
