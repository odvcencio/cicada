// Package ddsp implements a quantized harmonic-plus-noise neural voice.
// The pinned network predicts eight harmonic gains and a filtered-noise gain
// from fundamental frequency and linear loudness. All inference and oscillator
// arithmetic uses integers. State belongs to one render-thread owner.
package ddsp

const (
	Harmonics     = 8
	ControlFrames = 64
)

type Error string

func (e Error) Error() string { return string(e) }

// Synth owns its complete bounded render state. Its zero value is silent.
type Synth struct {
	sampleRate                      uint32
	phase, increment, noise, lastF0 uint32
	filteredNoise                   int32
	activeHarmonics                 uint8
	predictedF0                     uint32
	predictedLoudness               uint16
	ramping                         bool
	frame                           uint8
	start, current, target          [Harmonics + 1]int32
}

func New(sampleRate int) (Synth, error) {
	if sampleRate != 44100 && sampleRate != 48000 && sampleRate != 96000 {
		return Synth{}, Error("unsupported DDSP sample rate")
	}
	s := Synth{sampleRate: uint32(sampleRate)}
	s.Reset()
	return s, nil
}

// Reset restores the pinned noise seed and oscillator/control state.
func (s *Synth) Reset() {
	sr := s.sampleRate
	*s = Synth{sampleRate: sr, noise: 0x9e3779b9}
}

// Predict returns Q15 amplitudes from millihertz and Q15 linear loudness.
// The trained domain is 80..1600 Hz; higher controls saturate at 2048 Hz.
//
//go:noinline
func Predict(f0MilliHz uint32, loudness uint16) (out [Harmonics + 1]int32) {
	if f0MilliHz > 2048000 {
		f0MilliHz = 2048000
	}
	if loudness > 32767 {
		loudness = 32767
	}
	x := [2]int32{int32(uint64(f0MilliHz) * 32767 / 2048000), int32(loudness)}
	var hidden [8]int32
	for i := range hidden {
		sum := int32(hiddenBias[i]) << 15
		for j := range x {
			sum += int32(inputWeights[i][j]) * x[j]
		}
		hidden[i] = unit(sum >> 13)
	}
	for i := range out {
		sum := int32(outputBias[i]) << 15
		for j := range hidden {
			sum += int32(outputWeights[i][j]) * hidden[j]
		}
		out[i] = unit(sum >> 13)
	}
	// Exact silence is a control contract, independent of quantization error.
	if f0MilliHz == 0 || loudness == 0 {
		clear(out[:])
	}
	return out
}

func unit(x int32) int32 {
	if x < 0 {
		return 0
	}
	if x > 32767 {
		return 32767
	}
	return int32(x)
}

func sine(phase uint32) int32 {
	i := phase >> 24
	a, b := int32(sineTable[i]), int32(sineTable[i+1])
	// Adjacent table entries differ by at most 805; interpolation fits int32.
	return a + ((b - a) * int32((phase>>8)&65535) >> 16)
}

// Next renders one signed Q15 sample. Neural controls are sampled every 64
// frames and linearly interpolated. Calls and block partitions never allocate.
//
//go:noinline
func (s *Synth) Next(f0MilliHz uint32, loudness uint16) int16 {
	if s.sampleRate == 0 {
		return 0
	}
	limit := s.sampleRate * 490
	if f0MilliHz > limit {
		f0MilliHz = limit
	}
	if f0MilliHz != s.lastF0 {
		s.increment = uint32((uint64(f0MilliHz) << 32) / (uint64(s.sampleRate) * 1000))
		s.lastF0 = f0MilliHz
		s.activeHarmonics = Harmonics
		if f0MilliHz != 0 {
			count := (limit - 1) / f0MilliHz
			if count < Harmonics {
				s.activeHarmonics = uint8(count)
			}
		}
	}
	if s.frame == 0 {
		if f0MilliHz != s.predictedF0 || loudness != s.predictedLoudness {
			s.start = s.current
			s.target = Predict(f0MilliHz, loudness)
			s.predictedF0, s.predictedLoudness = f0MilliHz, loudness
			s.ramping = s.current != s.target
		}
	}
	s.frame++
	var sum int32
	if s.ramping {
		for i := range s.current {
			s.current[i] = s.start[i] + (s.target[i]-s.start[i])*int32(s.frame)/ControlFrames
		}
	}
	for i := 0; i < int(s.activeHarmonics); i++ {
		// Q15 products fit int32; round each partial before the bounded sum.
		sum += (s.current[i]*sine(s.phase*uint32(i+1)) + 16384) >> 15
	}
	x := s.noise
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	s.noise = x
	s.filteredNoise += (int32(int16(x>>16)) - s.filteredNoise) >> 2
	sum += (s.current[Harmonics]*s.filteredNoise + 16384) >> 15
	s.phase += s.increment
	if s.frame == ControlFrames {
		s.frame = 0
		s.ramping = false
	}
	// Mute invalid/zero controls immediately, while maintaining the time base.
	if f0MilliHz == 0 || loudness == 0 {
		return 0
	}
	y := sum
	if y > 32767 {
		y = 32767
	}
	if y < -32768 {
		y = -32768
	}
	return int16(y)
}

// NextFloat bridges graph controls to the integer model. Q15 to float32 is an
// exact power-of-two conversion; no floating-point sums occur in inference.
//
//go:noinline
func (s *Synth) NextFloat(f0Hz, loudness float32) float32 {
	if !(f0Hz > 0) {
		f0Hz = 0
	}
	if f0Hz > float32(s.sampleRate)*0.49 {
		f0Hz = float32(s.sampleRate) * 0.49
	}
	if !(loudness > 0) {
		loudness = 0
	}
	if loudness > 1 {
		loudness = 1
	}
	f0 := uint32(f0Hz*1000 + 0.5)
	l := uint16(loudness*32767 + 0.5)
	return float32(s.Next(f0, l)) * (1.0 / 32768)
}
