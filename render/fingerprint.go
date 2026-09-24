package render

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"math/cmplx"
	"runtime"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

const FingerprintBands = 64
const fingerprintHeaderSize = 56
const maxFingerprintFFTSize = 16384

// Fingerprint stores 100 ms frames of log-spaced band energy in tenths of dB.
// FirstSamplesHash covers the first 4096 interleaved stereo float32 frames.
type Fingerprint struct {
	SampleRate       uint32
	Samples          uint32
	WindowSamples    uint32
	FirstSamplesHash [32]byte
	Frames           [][FingerprintBands]int16
}

type FingerprintDifference struct {
	MeanDB float64
	MaxDB  float64
}

// EngineFingerprint renders the first bars of a compiled project through the
// native float32 engine. The last partial audio block is rendered at its true
// length, so the fingerprint ends on the same musical sample at every host.
func EngineFingerprint(cfg engine.Config, bars int) (Fingerprint, error) {
	var result Fingerprint
	if bars < 1 || bars > 256 || cfg.SampleRate%10 != 0 {
		return result, fmt.Errorf("fingerprint requires 1 to 256 bars and a 100 ms-aligned sample rate")
	}
	if cfg.BPMMilli == 0 {
		cfg.BPMMilli = 120_000
	}
	clock, err := seq.NewClock(cfg.SampleRate, cfg.BPMMilli)
	if err != nil {
		return result, err
	}
	sampleCount := clock.SampleAtTick(int64(bars) * seq.TicksPerBar)
	if sampleCount < 1 || sampleCount > int64(^uint32(0)) {
		return result, fmt.Errorf("fingerprint sample count is out of range")
	}
	player, err := engine.New(cfg)
	if err != nil {
		return result, err
	}
	if !player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		return result, fmt.Errorf("fingerprint play command was rejected")
	}
	left := make([]float32, int(sampleCount))
	right := make([]float32, int(sampleCount))
	for at := 0; at < len(left); {
		frames := min(cfg.MaxBlock, len(left)-at)
		player.Render(left[at:at+frames], right[at:at+frames])
		at += frames
	}
	return FingerprintStereo(left, right, cfg.SampleRate)
}

// FingerprintStereo accepts finite planar float32 stereo samples.
func FingerprintStereo(left, right []float32, sampleRate int) (Fingerprint, error) {
	var result Fingerprint
	if len(left) == 0 || len(left) != len(right) || int64(len(left)) > int64(^uint32(0)) || sampleRate < 1 || sampleRate%10 != 0 {
		return result, fmt.Errorf("invalid fingerprint audio")
	}
	windowSamples := sampleRate / 10
	fftSize := 8192
	if windowSamples > fftSize {
		fftSize = 16384
	}
	if windowSamples > maxFingerprintFFTSize {
		return result, fmt.Errorf("fingerprint rate exceeds FFT capacity")
	}
	result.SampleRate = uint32(sampleRate)
	result.Samples = uint32(len(left))
	result.WindowSamples = uint32(windowSamples)
	first := min(len(left), 4096)
	hashInput := make([]byte, first*8)
	for i := 0; i < first; i++ {
		binary.LittleEndian.PutUint32(hashInput[i*8:], math.Float32bits(left[i]))
		binary.LittleEndian.PutUint32(hashInput[i*8+4:], math.Float32bits(right[i]))
	}
	result.FirstSamplesHash = sha256.Sum256(hashInput)
	result.Frames = make([][FingerprintBands]int16, (len(left)+windowSamples-1)/windowSamples)
	var window [maxFingerprintFFTSize]float64
	var windowSum float64
	for i := 0; i < windowSamples; i++ {
		window[i] = .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(windowSamples-1))
		windowSum += window[i]
	}
	var bandBins [FingerprintBands][]int
	for bin := 1; bin < fftSize/2; bin++ {
		frequency := float64(bin*sampleRate) / float64(fftSize)
		if frequency < 40 || frequency >= 16_000 {
			continue
		}
		band := int(math.Log(frequency/40) / math.Log(400) * FingerprintBands)
		band = max(0, min(band, FingerprintBands-1))
		bandBins[band] = append(bandBins[band], bin)
	}
	for band := range bandBins {
		if len(bandBins[band]) != 0 {
			continue
		}
		center := 40 * math.Pow(400, (float64(band)+.5)/FingerprintBands)
		bin := int(math.Round(center * float64(fftSize) / float64(sampleRate)))
		bandBins[band] = append(bandBins[band], max(1, min(bin, fftSize/2-1)))
	}
	var spectrum [2][maxFingerprintFFTSize]complex128
	for frame := range result.Frames {
		start := frame * windowSamples
		for channel := 0; channel < 2; channel++ {
			clear(spectrum[channel][:fftSize])
			input := left
			if channel == 1 {
				input = right
			}
			for i := 0; i < windowSamples && start+i < len(input); i++ {
				sample := input[start+i]
				if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
					return Fingerprint{}, fmt.Errorf("nonfinite fingerprint sample at %d", start+i)
				}
				spectrum[channel][i] = complex(float64(sample)*window[i], 0)
			}
			fft(spectrum[channel][:fftSize])
		}
		for band, bins := range bandBins {
			var power float64
			for _, bin := range bins {
				power += cmplx.Abs(spectrum[0][bin])*cmplx.Abs(spectrum[0][bin]) + cmplx.Abs(spectrum[1][bin])*cmplx.Abs(spectrum[1][bin])
			}
			power /= float64(len(bins)) * 2 * math.Pow(windowSum/2, 2)
			db := -120.0
			if power > 1e-12 {
				db = 10 * math.Log10(power)
			}
			result.Frames[frame][band] = int16(math.Round(max(-120, min(db, 120)) * 10))
		}
	}
	return result, nil
}

func fft(data []complex128) {
	n := len(data)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}
	for span := 2; span <= n; span <<= 1 {
		angle := -2 * math.Pi / float64(span)
		root := cmplx.Rect(1, angle)
		for base := 0; base < n; base += span {
			rotation := complex(1, 0)
			for i := 0; i < span/2; i++ {
				even, odd := data[base+i], data[base+i+span/2]*rotation
				data[base+i], data[base+i+span/2] = even+odd, even-odd
				rotation *= root
			}
		}
	}
}

func (f Fingerprint) MarshalBinary() ([]byte, error) {
	if f.SampleRate == 0 || f.WindowSamples == 0 || f.Samples == 0 || len(f.Frames) == 0 || int64(len(f.Frames)) > int64(^uint32(0)) {
		return nil, fmt.Errorf("invalid fingerprint")
	}
	data := make([]byte, fingerprintHeaderSize+len(f.Frames)*FingerprintBands*2)
	copy(data[:4], "CIFP")
	binary.LittleEndian.PutUint16(data[4:6], 1)
	binary.LittleEndian.PutUint16(data[6:8], FingerprintBands)
	binary.LittleEndian.PutUint32(data[8:12], f.SampleRate)
	binary.LittleEndian.PutUint32(data[12:16], f.Samples)
	binary.LittleEndian.PutUint32(data[16:20], f.WindowSamples)
	binary.LittleEndian.PutUint32(data[20:24], uint32(len(f.Frames)))
	copy(data[24:56], f.FirstSamplesHash[:])
	for frame, bands := range f.Frames {
		for band, value := range bands {
			binary.LittleEndian.PutUint16(data[fingerprintHeaderSize+(frame*FingerprintBands+band)*2:], uint16(value))
		}
	}
	return data, nil
}

func DecodeFingerprint(data []byte) (Fingerprint, error) {
	var result Fingerprint
	if len(data) < fingerprintHeaderSize || string(data[:4]) != "CIFP" || binary.LittleEndian.Uint16(data[4:6]) != 1 || binary.LittleEndian.Uint16(data[6:8]) != FingerprintBands {
		return result, fmt.Errorf("invalid fingerprint header")
	}
	result.SampleRate = binary.LittleEndian.Uint32(data[8:12])
	result.Samples = binary.LittleEndian.Uint32(data[12:16])
	result.WindowSamples = binary.LittleEndian.Uint32(data[16:20])
	frames := binary.LittleEndian.Uint32(data[20:24])
	if frames == 0 || frames > 4096 || result.SampleRate == 0 || result.WindowSamples != result.SampleRate/10 || result.Samples == 0 || int64(result.Samples) > int64(frames)*int64(result.WindowSamples) || int64(result.Samples) <= int64(frames-1)*int64(result.WindowSamples) || len(data) != fingerprintHeaderSize+int(frames)*FingerprintBands*2 {
		return Fingerprint{}, fmt.Errorf("invalid fingerprint dimensions")
	}
	copy(result.FirstSamplesHash[:], data[24:56])
	result.Frames = make([][FingerprintBands]int16, frames)
	for frame := range result.Frames {
		for band := range result.Frames[frame] {
			result.Frames[frame][band] = int16(binary.LittleEndian.Uint16(data[fingerprintHeaderSize+(frame*FingerprintBands+band)*2:]))
		}
	}
	return result, nil
}

func FingerprintDrift(golden, current Fingerprint) (FingerprintDifference, error) {
	var difference FingerprintDifference
	if golden.SampleRate != current.SampleRate || golden.Samples != current.Samples || golden.WindowSamples != current.WindowSamples || len(golden.Frames) == 0 || len(golden.Frames) != len(current.Frames) {
		return difference, fmt.Errorf("fingerprint dimensions differ")
	}
	for frame := range golden.Frames {
		for band := range golden.Frames[frame] {
			delta := math.Abs(float64(golden.Frames[frame][band]-current.Frames[frame][band])) / 10
			difference.MeanDB += delta
			difference.MaxDB = max(difference.MaxDB, delta)
		}
	}
	difference.MeanDB /= float64(len(golden.Frames) * FingerprintBands)
	return difference, nil
}

// CompareFingerprint enforces the spectral budget and the same-platform
// first-sample byte check for the linux/amd64 reference fixture.
func CompareFingerprint(golden, current Fingerprint) (FingerprintDifference, error) {
	difference, err := FingerprintDrift(golden, current)
	if err != nil {
		return difference, err
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && golden.FirstSamplesHash != current.FirstSamplesHash {
		return difference, fmt.Errorf("first 4096 stereo float32 samples differ on linux/amd64")
	}
	if difference.MeanDB > .5 || difference.MaxDB > 2 {
		return difference, fmt.Errorf("fingerprint drift: mean %.3f dB, maximum %.3f dB", difference.MeanDB, difference.MaxDB)
	}
	return difference, nil
}
