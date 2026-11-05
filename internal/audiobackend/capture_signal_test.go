package audiobackend

import (
	"math"
	"testing"
)

// captureSignal is a bounded, in-memory test marker. Each bit lasts 50 ms;
// 10 ms RMS windows permit unknown device latency and channel gain.
type captureSignal struct {
	rate, window, chip     int
	bits                   [127]bool
	wave, quadrature       []float32
	high, low              float32
	frame, captureFrame    uint64
	bins                   []float64
	inPhase, quadratureSum float64
	count                  int
}

func newCaptureSignal(rate, seconds int) *captureSignal {
	s := &captureSignal{rate: rate, window: rate / 100, chip: 5, wave: make([]float32, rate), quadrature: make([]float32, rate), high: .18, low: .015, bins: make([]float64, 0, seconds*100+100)}
	seed := uint32(0x5eed1234)
	for i := range s.bits {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		s.bits[i] = seed&1 != 0
	}
	for i := range s.wave {
		phase := 2 * math.Pi * 997 * float64(i) / float64(rate)
		s.wave[i], s.quadrature[i] = float32(math.Sin(phase)), float32(math.Cos(phase))
	}
	return s
}

func (s *captureSignal) output(out [][]float32) {
	if len(out) == 0 {
		return
	}
	for i := range out[0] {
		amp := s.low
		if s.bits[(s.frame/uint64(s.window*s.chip))%uint64(len(s.bits))] {
			amp = s.high
		}
		value := amp * s.wave[s.frame%uint64(s.rate)]
		for _, channel := range out {
			channel[i] = value
		}
		s.frame++
	}
}

// input runs on the recorder's writer goroutine; it never saves raw audio.
func (s *captureSignal) input(in [][]float32) {
	if len(in) == 0 {
		return
	}
	for i := range in[0] {
		v := float64(in[0][i])
		phase := s.captureFrame % uint64(s.rate)
		s.inPhase += v * float64(s.wave[phase])
		s.quadratureSum += v * float64(s.quadrature[phase])
		s.captureFrame++
		s.count++
		if s.count == s.window {
			s.bins = append(s.bins, math.Sqrt2*math.Hypot(s.inPhase, s.quadratureSum)/float64(s.window))
			s.inPhase, s.quadratureSum, s.count = 0, 0, 0
		}
	}
}

// match finds a repeating marker without assuming the device's buffering delay.
// It uses only the middle 10 ms of each 50 ms chip to avoid transitions.
func (s *captureSignal) match() (correlation, highRMS float64, highWindows, silentHigh int) {
	period := len(s.bits) * s.chip
	best := -2.0
	for offset := 0; offset < period; offset++ {
		var x, y, xx, yy, xy float64
		n := 0
		for i := 100; i+100 < len(s.bins); i++ {
			phase := (i + offset) % period
			if phase%s.chip != s.chip/2 {
				continue
			}
			want := 0.0
			if s.bits[phase/s.chip] {
				want = 1
			}
			got := s.bins[i]
			x, y, xx, yy, xy = x+want, y+got, xx+want*want, yy+got*got, xy+want*got
			n++
		}
		if n < 20 {
			continue
		}
		den := math.Sqrt((float64(n)*xx - x*x) * (float64(n)*yy - y*y))
		if den == 0 {
			continue
		}
		corr := (float64(n)*xy - x*y) / den
		if corr <= best {
			continue
		}
		best, highRMS, highWindows, silentHigh = corr, 0, 0, 0
		for i := 100; i+100 < len(s.bins); i++ {
			phase := (i + offset) % period
			if phase%s.chip != s.chip/2 || !s.bits[phase/s.chip] {
				continue
			}
			highRMS += s.bins[i]
			highWindows++
		}
		if highWindows > 0 {
			highRMS /= float64(highWindows)
		}
		for i := 100; i+100 < len(s.bins); i++ {
			phase := (i + offset) % period
			if phase%s.chip == s.chip/2 && s.bits[phase/s.chip] && s.bins[i] < highRMS*.1 {
				silentHigh++
			}
		}
	}
	if best < -1 {
		return 0, 0, 0, 0
	}
	return best, highRMS, highWindows, silentHigh
}

func TestCaptureSignalDetectsLoopbackAndMissingWindows(t *testing.T) {
	const rate = 48000
	signal, recorded := newCaptureSignal(rate, 10), newCaptureSignal(rate, 10)
	buffer := make([]float32, rate*10)
	signal.output([][]float32{buffer})
	// Simulate a delayed, attenuated loopback and one lost high marker chip.
	input := make([]float32, len(buffer))
	for i := 240; i < len(input); i++ {
		// A stronger unrelated low-frequency signal must not mask the marker.
		input[i] = buffer[i-240]*.1 + float32(.08*math.Sin(2*math.Pi*160*float64(i)/rate))
	}
	lostChip := 80
	for !signal.bits[lostChip%len(signal.bits)] {
		lostChip++
	}
	start := lostChip*(rate/20) + 240
	for i := start; i < start+rate/20; i++ {
		input[i] = 0
	}
	recorded.input([][]float32{input})
	corr, rms, windows, missing := recorded.match()
	if corr < .9 || rms < .008 || windows < 20 || missing < 1 {
		t.Fatalf("marker or dropout lost: correlation=%g rms=%g windows=%d silent=%d", corr, rms, windows, missing)
	}
	silence := newCaptureSignal(rate, 10)
	silence.input([][]float32{make([]float32, len(buffer))})
	if corr, rms, _, _ := silence.match(); corr != 0 || rms != 0 {
		t.Fatalf("silence looked like a marker: %g %g", corr, rms)
	}
}
