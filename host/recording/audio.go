// Package recording prepares user recordings outside the audio callback.
package recording

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"m31labs.dev/cicada/audioasset"
)

const MaxFrames = 8 << 20
const MaxInputBytes = 64 << 20

// A sample at or above clipLevel sits at digital full scale: 16-bit full scale
// is .99997 and 24-bit .9999999. Three in a row with the same value make a
// clipped run, which is a flat top. A lone peak at full scale is not clipping,
// and neither is the round crest of a loud note, whose samples differ even when
// they all lie close to full scale. A float recording may hold samples above
// full scale: they are clipped only if they stay flat.
const (
	clipLevel = .9999
	clipRun   = 3
)

// Audio keeps source coordinates and an exact checksum of the input WAV.
type Audio struct {
	PCM    []float32
	Rate   int
	SHA256 string
	// clipped has one bit per frame, set for the frames of every clipped run
	// that DecodeWAV found in a channel before mixing down. It is nil when
	// there are none, and for audio that did not come from DecodeWAV.
	clipped []uint64
}

// ClippedFrames counts the frames of PCM[start:end] that lie in a clipped run.
func (a Audio) ClippedFrames(start, end int) int {
	n := 0
	for i := max(0, start); i < min(end, len(a.PCM)); i++ {
		if i>>6 < len(a.clipped) && a.clipped[i>>6]>>(i&63)&1 != 0 {
			n++
		}
	}
	return n
}

// markClipped sets the bit of frame i, and of the frames just before it when it
// is the one that completes a run.
func (a *Audio) markClipped(i int, completes bool) {
	if a.clipped == nil {
		a.clipped = make([]uint64, (len(a.PCM)+63)/64)
	}
	from := i
	if completes {
		from = i - clipRun + 1
	}
	for ; from <= i; from++ {
		a.clipped[from>>6] |= 1 << (from & 63)
	}
}

func DecodeWAV(data []byte) (Audio, error) {
	var a Audio
	if len(data) > MaxInputBytes {
		return a, fmt.Errorf("recording exceeds 64 MiB")
	}
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(data))
	if err != nil {
		return a, err
	}
	if h.Frames > MaxFrames || h.RateHz > 192000 {
		return a, fmt.Errorf("recording exceeds frame or sample-rate limit")
	}
	a.Rate, a.SHA256 = h.RateHz, Digest(data)
	a.PCM = make([]float32, int(h.Frames))
	width, offset := h.BitDepth/8, int(h.DataOffset)
	// run counts, for each channel, the samples in a row that sit at full scale
	// with the value last.
	run := make([]int, h.Channels)
	last := make([]float64, h.Channels)
	for i := range a.PCM {
		var sum float64
		for ch := 0; ch < h.Channels; ch++ {
			b := data[offset : offset+width]
			offset += width
			var value float64
			switch {
			case h.Encoding == "float":
				value = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
			case width == 2:
				value = float64(int16(binary.LittleEndian.Uint16(b))) / 32768
			case width == 3:
				n := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
				value = float64(n<<8>>8) / 8388608
			default:
				value = float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
			}
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 8 {
				return Audio{}, fmt.Errorf("invalid PCM at frame %d", i)
			}
			switch {
			case math.Abs(value) < clipLevel:
				run[ch] = 0
			case run[ch] > 0 && value == last[ch]:
				if run[ch]++; run[ch] >= clipRun {
					a.markClipped(i, run[ch] == clipRun)
				}
			default:
				run[ch], last[ch] = 1, value
			}
			sum += value
		}
		a.PCM[i] = float32(sum / float64(h.Channels))
	}
	return a, nil
}

func Digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// EncodeWAV emits deterministic mono float32 PCM without dither or timestamps.
func EncodeWAV(pcm []float32, rate int) []byte {
	b := make([]byte, 44+4*len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 3)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*4))
	binary.LittleEndian.PutUint16(b[32:], 4)
	binary.LittleEndian.PutUint16(b[34:], 32)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(pcm)*4))
	for i, x := range pcm {
		binary.LittleEndian.PutUint32(b[44+i*4:], math.Float32bits(x))
	}
	return b
}
