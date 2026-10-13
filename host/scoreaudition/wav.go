package scoreaudition

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// readFloatWAV reads a stereo IEEE float32 WAV as mono (channel mean).
func readFloatWAV(path string) (mono []float32, rate int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("%s: not a WAV file", path)
	}
	var tag, channels, bits uint16
	var raw []byte
	for pos := 12; pos+8 <= len(data); {
		id, size := string(data[pos:pos+4]), int(binary.LittleEndian.Uint32(data[pos+4:pos+8]))
		body := data[pos+8 : min(len(data), pos+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, fmt.Errorf("%s: short fmt chunk", path)
			}
			tag = binary.LittleEndian.Uint16(body[0:2])
			channels = binary.LittleEndian.Uint16(body[2:4])
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = binary.LittleEndian.Uint16(body[14:16])
		case "data":
			raw = body
		}
		pos += 8 + size + size&1
	}
	if tag != 3 || bits != 32 || channels < 1 || rate <= 0 {
		return nil, 0, fmt.Errorf("%s: expected float32 WAV", path)
	}
	frames := len(raw) / (4 * int(channels))
	mono = make([]float32, frames)
	for i := range mono {
		var sum float32
		for c := 0; c < int(channels); c++ {
			sum += math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*int(channels)+c)*4:]))
		}
		mono[i] = sum / float32(channels)
	}
	return mono, rate, nil
}
