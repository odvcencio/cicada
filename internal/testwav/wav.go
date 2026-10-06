// Package testwav generates small deterministic WAV fixtures for tests and examples.
package testwav

import "encoding/binary"

func Bytes(rate, channels, bits, frames, encoding int) []byte {
	alignment := channels * bits / 8
	payload := alignment * frames
	data := make([]byte, 44+payload+(payload&1))
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], uint16(encoding))
	binary.LittleEndian.PutUint16(data[22:], uint16(channels))
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(rate*alignment))
	binary.LittleEndian.PutUint16(data[32:], uint16(alignment))
	binary.LittleEndian.PutUint16(data[34:], uint16(bits))
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(payload))
	// A short bounded ramp distinguishes the payload from an all-zero fixture.
	if bits == 16 && encoding == 1 {
		for i := 0; i < frames; i++ {
			for ch := 0; ch < channels; ch++ {
				binary.LittleEndian.PutUint16(data[44+(i*channels+ch)*2:], uint16(int16(i%256-128)*32))
			}
		}
	}
	return data
}
