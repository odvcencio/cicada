package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
)

const ditherIncrement uint64 = 0xda3e39cb94b95bdb

type pcg32 struct{ state uint64 }

func newPCG32(seed uint64) pcg32 {
	rng := pcg32{state: seed + ditherIncrement}
	rng.next()
	rng.state += seed
	rng.next()
	return rng
}

func (rng *pcg32) next() uint32 {
	old := rng.state
	rng.state = old*6364136223846793005 + ditherIncrement
	x := uint32(((old >> 18) ^ old) >> 27)
	return bits.RotateLeft32(x, -int(old>>59))
}

type wavEncoder struct {
	bits   int
	dither bool
	gain   float32
	rng    [2]pcg32
}

func newWAVEncoder(bits int, dither bool, seed uint64, gain float32) (wavEncoder, error) {
	if bits != 16 && bits != 24 && bits != 32 {
		return wavEncoder{}, fmt.Errorf("WAV bits must be 16, 24, or 32 float")
	}
	if math.IsNaN(float64(gain)) || math.IsInf(float64(gain), 0) || gain <= 0 {
		return wavEncoder{}, fmt.Errorf("invalid WAV output gain")
	}
	return wavEncoder{
		bits: bits, dither: dither && bits != 32, gain: gain,
		rng: [2]pcg32{
			newPCG32(seed ^ 0xD17E0000),
			newPCG32(seed ^ 0xD17E0000 ^ 1),
		},
	}, nil
}

func (encoder *wavEncoder) frameBytes() int { return encoder.bits / 4 }

// advanceDither keeps a range render's noise aligned with a full-song render.
func (encoder *wavEncoder) advanceDither() {
	if !encoder.dither {
		return
	}
	for channel := range encoder.rng {
		encoder.rng[channel].next()
		encoder.rng[channel].next()
	}
}

func (encoder *wavEncoder) writeFrame(dst []byte, left, right float32, ceiling float64, report *Report) {
	for channel, value := range [...]float32{left, right} {
		value *= encoder.gain
		if value == 0 {
			value = 0 // normalize negative zero in float WAV output
		}
		abs := math.Abs(float64(value))
		if abs > float64(report.OutputPeak) {
			report.OutputPeak = float32(abs)
		}
		if abs >= ceiling-1e-7 {
			report.CeilingSamples++
		}
		if abs > 1 {
			report.ClippedSamples++
		}
		if encoder.bits == 32 {
			binary.LittleEndian.PutUint32(dst[channel*4:], math.Float32bits(value))
			continue
		}
		maxPCM := float64(int64(1)<<(encoder.bits-1) - 1)
		quantized := float64(value) * maxPCM
		if encoder.dither {
			first := encoder.rng[channel].next() >> 8
			second := encoder.rng[channel].next() >> 8
			quantized += float64(int64(first)-int64(second)) / (1 << 24)
		}
		pcm := int32(math.RoundToEven(quantized))
		pcm = max(-int32(maxPCM)-1, min(int32(maxPCM), pcm))
		index := channel * (encoder.bits / 8)
		dst[index] = byte(pcm)
		dst[index+1] = byte(pcm >> 8)
		if encoder.bits == 24 {
			dst[index+2] = byte(pcm >> 16)
		}
	}
}

func writeWAVHeader(w io.Writer, sampleRate, bits int, dataBytes uint32) error {
	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], dataBytes+60)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	format := uint16(1)
	if bits == 32 {
		format = 3
	}
	binary.LittleEndian.PutUint16(header[20:22], format)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	frameBytes := bits / 4
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*frameBytes))
	binary.LittleEndian.PutUint16(header[32:34], uint16(frameBytes))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bits))
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataBytes)
	n, err := w.Write(header[:])
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}
