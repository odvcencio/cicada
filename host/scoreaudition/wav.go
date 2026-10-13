package scoreaudition

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// stereo is a pair of channels of equal length. Analysis keeps the channels
// apart: averaging them first would cancel opposite-polarity material.
type stereo struct{ L, R []float32 }

func (s stereo) Len() int { return len(s.L) }

func (s stereo) Slice(lo, hi int) stereo { return stereo{s.L[lo:hi], s.R[lo:hi]} }

// readFloatWAV reads a float32 WAV. A mono file reads as two equal channels;
// more than two channels keep the first two.
func readFloatWAV(path string) (sig stereo, rate int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sig, 0, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return sig, 0, fmt.Errorf("%s: not a WAV file", path)
	}
	var tag, channels, bits uint16
	var raw []byte
	for pos := 12; pos+8 <= len(data); {
		id, size := string(data[pos:pos+4]), int(binary.LittleEndian.Uint32(data[pos+4:pos+8]))
		body := data[pos+8 : min(len(data), pos+8+size)]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return sig, 0, fmt.Errorf("%s: short fmt chunk", path)
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
		return sig, 0, fmt.Errorf("%s: expected float32 WAV", path)
	}
	n := int(channels)
	frames := len(raw) / (4 * n)
	sig = stereo{L: make([]float32, frames), R: make([]float32, frames)}
	for i := 0; i < frames; i++ {
		sig.L[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*n*4:]))
		if n > 1 {
			sig.R[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[(i*n+1)*4:]))
		} else {
			sig.R[i] = sig.L[i]
		}
	}
	return sig, rate, nil
}
