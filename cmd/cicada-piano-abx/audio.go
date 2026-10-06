package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"m31labs.dev/cicada/kernel/loudness"
)

type audio struct{ left, right []float32 }

type measurement struct {
	LUFS          float64 `json:"integrated_lufs"`
	SamplePeak    float64 `json:"sample_peak_dbfs"`
	TruePeak      float64 `json:"true_peak_dbtp"`
	GainDB        float64 `json:"gain_db"`
	RenderSeconds float64 `json:"render_wall_seconds"`
}

func render(v instrument, f fixture) (audio, time.Duration, error) {
	v.Reset()
	a := audio{make([]float32, f.Frames), make([]float32, f.Frames)}
	index := 0
	start := time.Now()
	for frame := range a.left {
		for index < len(f.Events) && f.Events[index].Frame == frame {
			e := f.Events[index]
			var err error
			switch e.Kind {
			case "on":
				err = v.NoteOn(e.Note, e.Velocity)
			case "off":
				v.NoteOff(e.Note)
			case "pedal":
				err = v.SetSustain(e.Pedal)
			default:
				return audio{}, 0, fmt.Errorf("unknown performance event %q", e.Kind)
			}
			if err != nil {
				return audio{}, 0, err
			}
			index++
		}
		a.left[frame], a.right[frame] = v.NextStereo()
	}
	if index != len(f.Events) {
		return audio{}, 0, fmt.Errorf("events outside the performance")
	}
	return a, time.Since(start), nil
}

func measure(a audio) (measurement, error) {
	m, err := loudness.New(outputRate)
	if err != nil {
		return measurement{}, err
	}
	if err := m.ProcessBlock(a.left, a.right); err != nil {
		return measurement{}, err
	}
	if err := m.Finish(); err != nil {
		return measurement{}, err
	}
	r := m.Metrics()
	if math.IsInf(r.IntegratedLUFS, 0) || math.IsNaN(r.IntegratedLUFS) {
		return measurement{}, fmt.Errorf("audio has no gated loudness")
	}
	return measurement{LUFS: r.IntegratedLUFS, SamplePeak: r.SamplePeakDBFS, TruePeak: r.TruePeakDBTP}, nil
}

// Match encoded PCM, rather than assuming a gain change leaves gates unchanged.
// Peak headroom reduces the common target and never changes either waveform.
func matchPair(a, b audio, target float64) (audio, audio, measurement, measurement, error) {
	ma, err := measure(a)
	if err != nil {
		return audio{}, audio{}, measurement{}, measurement{}, err
	}
	mb, err := measure(b)
	if err != nil {
		return audio{}, audio{}, measurement{}, measurement{}, err
	}
	target = math.Min(target, math.Min(ma.LUFS-1.01-ma.TruePeak, mb.LUFS-1.01-mb.TruePeak))
	x, mx, err := normalize(a, ma, target)
	if err != nil {
		return audio{}, audio{}, measurement{}, measurement{}, err
	}
	y, my, err := normalize(b, mb, target)
	if err != nil {
		return audio{}, audio{}, measurement{}, measurement{}, err
	}
	if math.Abs(mx.LUFS-my.LUFS) > .1 {
		return audio{}, audio{}, measurement{}, measurement{}, fmt.Errorf("encoded loudness differs by %.4f LU", math.Abs(mx.LUFS-my.LUFS))
	}
	if mx.TruePeak > -1 || my.TruePeak > -1 {
		return audio{}, audio{}, measurement{}, measurement{}, fmt.Errorf("encoded true peak exceeds -1 dBTP")
	}
	return x, y, mx, my, nil
}

func normalize(source audio, before measurement, target float64) (audio, measurement, error) {
	result := audio{make([]float32, len(source.left)), make([]float32, len(source.right))}
	gainDB := target - before.LUFS
	for attempt := 0; attempt < 5; attempt++ {
		gain := math.Pow(10, gainDB/20)
		for i := range source.left {
			result.left[i] = pcm24(float64(source.left[i]) * gain)
			result.right[i] = pcm24(float64(source.right[i]) * gain)
		}
		m, err := measure(result)
		if err != nil {
			return audio{}, measurement{}, err
		}
		m.GainDB = gainDB
		if math.Abs(m.LUFS-target) <= .025 {
			return result, m, nil
		}
		gainDB += target - m.LUFS
	}
	return audio{}, measurement{}, fmt.Errorf("encoded loudness matching did not converge")
}

func pcm24(value float64) float32 {
	return float32(math.Max(-8388608, math.Min(8388607, math.RoundToEven(value*8388608))) / 8388608)
}

func encodeWAV(a audio) ([]byte, error) {
	if len(a.left) != len(a.right) || len(a.left) > math.MaxUint32/6 {
		return nil, fmt.Errorf("invalid stereo PCM length")
	}
	b := make([]byte, 44+len(a.left)*6)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 2)
	binary.LittleEndian.PutUint32(b[24:], outputRate)
	binary.LittleEndian.PutUint32(b[28:], outputRate*6)
	binary.LittleEndian.PutUint16(b[32:], 6)
	binary.LittleEndian.PutUint16(b[34:], 24)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(a.left)*6))
	for i := range a.left {
		for channel, value := range []float32{a.left[i], a.right[i]} {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value <= -1 || value >= 1 {
				return nil, fmt.Errorf("non-finite or clipped output at frame %d", i)
			}
			n := int32(math.RoundToEven(float64(value) * 8388608))
			offset := 44 + i*6 + channel*3
			b[offset], b[offset+1], b[offset+2] = byte(n), byte(n>>8), byte(n>>16)
		}
	}
	return b, nil
}
