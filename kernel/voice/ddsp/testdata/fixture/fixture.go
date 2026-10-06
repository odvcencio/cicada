// Package fixture exercises integer controls, smoothing, phase and noise with
// identical call schedules on native Go and the TinyGo kernel toolchain.
package fixture

import "m31labs.dev/cicada/kernel/voice/ddsp"

const Frames = 4096
const Cases = 3

func Render(index, blockSize int, output []float32) bool {
	if index < 0 || index >= Cases || blockSize < 1 || len(output) != Frames {
		return false
	}
	s, err := ddsp.New([Cases]int{44100, 48000, 96000}[index])
	if err != nil {
		return false
	}
	for block := 0; block < Frames; block += blockSize {
		end := block + blockSize
		if end > Frames {
			end = Frames
		}
		for i := block; i < end; i++ {
			f0 := uint32(80000 + (i/127)*41000)
			l := uint16(1000 + (i/83)*600)
			if i%991 < 17 {
				l = 0
			}
			if i == 2048 {
				s.Reset()
			}
			if i%2 == 0 {
				output[i] = float32(s.Next(f0, l)) * (1.0 / 32768)
			} else {
				output[i] = s.NextFloat(float32(f0)/1000+0.0005, float32(l)/32767)
			}
		}
	}
	return true
}
