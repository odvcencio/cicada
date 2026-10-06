// Package audition renders a score to owned stereo PCM for offline processing.
// It is a host operation and must not be called from an audio callback.
package audition

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
	"math"
)

type Audio struct {
	Rate        int
	Left, Right []float32
}

type Processor interface {
	Process(float32, float32) (float32, float32)
	LatencyFrames() int
	Fault() bool
}

func Render(source []byte, rate, bars int, tail float64) (Audio, error) {
	score, ds := notation.Parse(source)
	for _, d := range ds {
		if d.Severity == "error" {
			return Audio{}, fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	if score == nil {
		return Audio{}, fmt.Errorf("empty score")
	}
	var wav bytes.Buffer
	report, err := render.WAV(score, render.Options{SampleRate: rate, Bits: 32, Bars: bars, TailSec: tail}, &wav)
	if err != nil {
		return Audio{}, err
	}
	if report.Frames > int64(rate)*600 {
		return Audio{}, fmt.Errorf("audition exceeds ten minutes")
	}
	audio := Audio{Rate: rate, Left: make([]float32, report.Frames), Right: make([]float32, report.Frames)}
	for i := range audio.Left {
		audio.Left[i] = math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[44+i*8:]))
		audio.Right[i] = math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[48+i*8:]))
	}
	return audio, nil
}

// Process compensates deterministic processor latency, including its final
// delayed frames, so before/after files remain aligned and have equal length.
func (a Audio) Process(p Processor) error {
	latency := p.LatencyFrames()
	if latency < 0 || latency > a.Rate*2 {
		return fmt.Errorf("invalid audition processor latency")
	}
	// A separate output preserves unread input when a lookahead processor delays.
	left, right := make([]float32, len(a.Left)), make([]float32, len(a.Right))
	for frame := 0; frame < len(a.Left)+latency; frame++ {
		var l, r float32
		if frame < len(a.Left) {
			l, r = a.Left[frame], a.Right[frame]
		}
		l, r = p.Process(l, r)
		if p.Fault() || math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
			return fmt.Errorf("processor fault at frame %d", frame)
		}
		if frame >= latency {
			left[frame-latency], right[frame-latency] = l, r
		}
	}
	copy(a.Left, left)
	copy(a.Right, right)
	return nil
}

func (a Audio) WriteWAV(w io.Writer) error {
	if len(a.Left) != len(a.Right) || len(a.Left) > math.MaxUint32/8 || a.Rate < 8000 || a.Rate > 192000 {
		return fmt.Errorf("invalid stereo PCM")
	}
	var header [44]byte
	copy(header[:], "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(36+len(a.Left)*8))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	binary.LittleEndian.PutUint16(header[20:], 3)
	binary.LittleEndian.PutUint16(header[22:], 2)
	binary.LittleEndian.PutUint32(header[24:], uint32(a.Rate))
	binary.LittleEndian.PutUint32(header[28:], uint32(a.Rate*8))
	binary.LittleEndian.PutUint16(header[32:], 8)
	binary.LittleEndian.PutUint16(header[34:], 32)
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(a.Left)*8))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	var block [8192]byte
	for start := 0; start < len(a.Left); start += 1024 {
		n := min(1024, len(a.Left)-start)
		for i := 0; i < n; i++ {
			l, r := a.Left[start+i], a.Right[start+i]
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				return fmt.Errorf("nonfinite output PCM")
			}
			binary.LittleEndian.PutUint32(block[i*8:], math.Float32bits(l))
			binary.LittleEndian.PutUint32(block[i*8+4:], math.Float32bits(r))
		}
		if _, err := w.Write(block[:n*8]); err != nil {
			return err
		}
	}
	return nil
}
