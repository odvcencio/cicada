package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"m31labs.dev/cicada/kernel/seq"
)

type VerifyOptions struct {
	SampleRate int
	Bits       int
	Bars       int
	From       int
	TailSec    float64
	PeakMaxDB  float64
	DCMaxDB    float64
}

type VerifyReport struct {
	Frames         int64
	Peak           float64
	PeakDB         float64
	DC             float64
	DCDB           float64
	ClippedSamples int64
}

// VerifyWAV checks the generated WAV container, musical duration, and signal
// ceilings from the file bytes. The trailing cica chunk carries tempo and bars.
func VerifyWAV(path string, opts VerifyOptions) (VerifyReport, error) {
	var report VerifyReport
	if opts.SampleRate != 44_100 && opts.SampleRate != 48_000 && opts.SampleRate != 96_000 {
		return report, fmt.Errorf("unsupported sample rate")
	}
	if opts.Bits != 16 && opts.Bits != 24 && opts.Bits != 32 || opts.Bars < 1 || opts.Bars > 256 || opts.From < 0 || opts.From+opts.Bars > 256 {
		return report, fmt.Errorf("verify-wav requires 16, 24, or 32 float bits and a bar range within 256 bars")
	}
	if math.IsNaN(opts.TailSec) || math.IsInf(opts.TailSec, 0) || opts.TailSec < 0 || opts.TailSec > 10 {
		return report, fmt.Errorf("tail must be 0 to 10 seconds")
	}
	file, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return report, err
	}
	if info.Size() < 68 {
		return report, fmt.Errorf("WAV is too short")
	}
	var header [44]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return report, err
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" || string(header[12:16]) != "fmt " || string(header[36:40]) != "data" {
		return report, fmt.Errorf("unsupported WAV chunk layout")
	}
	get16 := func(start int) uint16 { return binary.LittleEndian.Uint16(header[start : start+2]) }
	get32 := func(start int) uint32 { return binary.LittleEndian.Uint32(header[start : start+4]) }
	frameBytes := opts.Bits / 4
	format := uint16(1)
	if opts.Bits == 32 {
		format = 3
	}
	if get32(4) != uint32(info.Size()-8) || get32(16) != 16 || get16(20) != format || get16(22) != 2 || get32(24) != uint32(opts.SampleRate) || get32(28) != uint32(opts.SampleRate*frameBytes) || get16(32) != uint16(frameBytes) || get16(34) != uint16(opts.Bits) {
		return report, fmt.Errorf("WAV header does not match stereo %d-bit format at %d Hz", opts.Bits, opts.SampleRate)
	}
	dataBytes := int64(get32(40))
	if dataBytes%int64(frameBytes) != 0 || info.Size() != 44+dataBytes+24 {
		return report, fmt.Errorf("WAV size or frame alignment is invalid")
	}
	report.Frames = dataBytes / int64(frameBytes)
	var metadata [24]byte
	if _, err := file.ReadAt(metadata[:], 44+dataBytes); err != nil {
		return report, err
	}
	if string(metadata[:4]) != "cica" || binary.LittleEndian.Uint32(metadata[4:8]) != 16 || binary.LittleEndian.Uint32(metadata[8:12]) != 1 {
		return report, fmt.Errorf("missing Cicada timing metadata")
	}
	tempoMilli := binary.LittleEndian.Uint32(metadata[12:16])
	bars := binary.LittleEndian.Uint32(metadata[16:20])
	tailFrames := binary.LittleEndian.Uint32(metadata[20:24])
	wantTail := uint32(math.Ceil(opts.TailSec * float64(opts.SampleRate)))
	if bars != uint32(opts.Bars) || tailFrames != wantTail {
		return report, fmt.Errorf("WAV bar or tail metadata differs from request")
	}
	clock, err := seq.NewClock(opts.SampleRate, int64(tempoMilli))
	if err != nil {
		return report, fmt.Errorf("invalid WAV tempo metadata: %w", err)
	}
	wantFrames := clock.SampleAtTick(int64(opts.From+int(bars))*seq.TicksPerBar) - clock.SampleAtTick(int64(opts.From)*seq.TicksPerBar) + int64(tailFrames)
	if report.Frames != wantFrames {
		return report, fmt.Errorf("WAV duration: got %d frames, want %d", report.Frames, wantFrames)
	}
	var sums [2]float64
	var peak float64
	var block [4096 * 8]byte
	for remaining := report.Frames; remaining > 0; {
		frames := min(remaining, 4096)
		buf := block[:int(frames)*frameBytes]
		if _, err := io.ReadFull(file, buf); err != nil {
			return report, err
		}
		for i := 0; i < len(buf); i += frameBytes {
			for channel := 0; channel < 2; channel++ {
				j := i + channel*(opts.Bits/8)
				var sample float64
				if opts.Bits == 32 {
					sample = float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[j : j+4])))
					if math.IsNaN(sample) || math.IsInf(sample, 0) {
						return report, fmt.Errorf("nonfinite float WAV sample")
					}
				} else if opts.Bits == 16 {
					value := int16(binary.LittleEndian.Uint16(buf[j : j+2]))
					if value == 32767 || value == -32768 {
						report.ClippedSamples++
					}
					sample = float64(value) / 32768
				} else {
					value := int32(buf[j]) | int32(buf[j+1])<<8 | int32(buf[j+2])<<16
					if value&0x800000 != 0 {
						value |= ^int32(0xffffff)
					}
					if value == 8388607 || value == -8388608 {
						report.ClippedSamples++
					}
					sample = float64(value) / 8388608
				}
				sums[channel] += sample
				peak = max(peak, math.Abs(sample))
			}
		}
		remaining -= frames
	}
	report.Peak = peak
	report.PeakDB = amplitudeDB(peak)
	report.DC = max(math.Abs(sums[0]/float64(report.Frames)), math.Abs(sums[1]/float64(report.Frames)))
	report.DCDB = amplitudeDB(report.DC)
	if report.ClippedSamples != 0 {
		return report, fmt.Errorf("WAV contains %d full-scale clipped integer samples", report.ClippedSamples)
	}
	if report.PeakDB > opts.PeakMaxDB {
		return report, fmt.Errorf("peak %.2f dBFS exceeds %.2f dBFS", report.PeakDB, opts.PeakMaxDB)
	}
	if report.DCDB > opts.DCMaxDB {
		return report, fmt.Errorf("DC %.2f dBFS exceeds %.2f dBFS", report.DCDB, opts.DCMaxDB)
	}
	return report, nil
}

func amplitudeDB(value float64) float64 {
	if value == 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(value)
}
