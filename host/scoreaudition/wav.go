package scoreaudition

import (
	"encoding/binary"
	"fmt"
	"io"
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

// readFloatWAVFrames reads a float32 WAV's samples at a few frames without
// loading the file: [frame][channel], zero past the end. A mono file gives
// both channels the same sample.
func readFloatWAVFrames(path string, frames []int) ([][2]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var head [12]byte
	if _, err := io.ReadFull(f, head[:]); err != nil || string(head[:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s: not a WAV file", path)
	}
	var tag, channels, bits uint16
	var dataAt, dataSize int64
	for pos := int64(12); dataAt == 0; {
		var chunk [8]byte
		if _, err := f.ReadAt(chunk[:], pos); err != nil {
			return nil, fmt.Errorf("%s: no data chunk", path)
		}
		id, size := string(chunk[:4]), int64(binary.LittleEndian.Uint32(chunk[4:8]))
		switch id {
		case "fmt ":
			var body [16]byte
			if size < 16 {
				return nil, fmt.Errorf("%s: short fmt chunk", path)
			}
			if _, err := f.ReadAt(body[:], pos+8); err != nil {
				return nil, err
			}
			tag, channels, bits = binary.LittleEndian.Uint16(body[0:2]), binary.LittleEndian.Uint16(body[2:4]), binary.LittleEndian.Uint16(body[14:16])
		case "data":
			dataAt, dataSize = pos+8, size
		}
		pos += 8 + size + size&1
	}
	if tag != 3 || bits != 32 || channels < 1 {
		return nil, fmt.Errorf("%s: expected float32 WAV", path)
	}
	stride := int64(channels) * 4
	out := make([][2]float64, len(frames))
	buf := make([]byte, stride)
	for i, frame := range frames {
		at := int64(frame) * stride
		if frame < 0 || at+stride > dataSize {
			continue
		}
		if _, err := f.ReadAt(buf, dataAt+at); err != nil {
			return nil, err
		}
		out[i][0] = float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[0:4])))
		out[i][1] = out[i][0]
		if channels > 1 {
			out[i][1] = float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[4:8])))
		}
	}
	return out, nil
}
