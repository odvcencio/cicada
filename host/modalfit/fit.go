// Package modalfit estimates resonances and decay times outside audio rendering.
package modalfit

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/cmplx"
	"sort"

	"m31labs.dev/cicada/host/recording"
)

const MaxModes = 10
const Format = "cicada.modal-model/1"

type Mode struct {
	FrequencyHz     float64 `json:"frequencyHz"`
	Ratio           float64 `json:"ratio"`
	Weight          float64 `json:"weight"`
	T60             float64 `json:"t60"`
	DecayConfidence float64 `json:"decayConfidence"`
}

// Model stores measured parameters, never microphone/device identities.
type Model struct {
	Format       string  `json:"format"`
	License      string  `json:"license"`
	SourceSHA256 string  `json:"sourceSHA256"`
	HitSHA256    string  `json:"hitSHA256"`
	SourceRate   int     `json:"sourceRate"`
	RootHz       float64 `json:"rootHz"`
	RootMIDI     int     `json:"rootMIDI"`
	NoiseMix     float64 `json:"noiseMix"`
	Modes        []Mode  `json:"modes"`
}

func Fit(pcm []float32, rate int, sourceSHA256 string) (Model, error) {
	var model Model
	hash, err := hex.DecodeString(sourceSHA256)
	if err != nil || len(hash) != 32 || rate < 8000 || rate > 192000 || len(pcm) < rate/100 || len(pcm) > 8<<20 {
		return model, fmt.Errorf("fit needs a checksummed hit of at least 10 ms at 8–192 kHz")
	}
	peak := 0.0
	for _, x := range pcm {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || math.Abs(float64(x)) > 8 {
			return model, fmt.Errorf("hit contains invalid PCM")
		}
		peak = max(peak, math.Abs(float64(x)))
	}
	if peak < 1e-5 {
		return model, fmt.Errorf("hit is silent")
	}
	// A short attack window retains rapidly damped modes that a long FFT
	// would hide beneath the strongest, slowly ringing resonance.
	length := min(len(pcm), int(float64(rate)*.06))
	n := 1
	for n < length*4 {
		n *= 2
	}
	data := make([]complex128, n)
	var mean float64
	for _, x := range pcm[:length] {
		mean += float64(x)
	}
	mean /= float64(length)
	for i, x := range pcm[:length] {
		window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(length-1))
		data[i] = complex((float64(x)-mean)*window, 0)
	}
	fft(data)
	power := make([]float64, n/2)
	maximum, total := 0.0, 0.0
	for i := range power {
		power[i] = real(data[i])*real(data[i]) + imag(data[i])*imag(data[i])
		total += power[i]
		if float64(i)*float64(rate)/float64(n) >= 60 {
			maximum = max(maximum, power[i])
		}
	}
	type candidate struct{ frequency, power float64 }
	var peaks []candidate
	low, high := max(2, int(60*float64(n)/float64(rate))), min(len(power)-2, int(.45*float64(n)))
	for i := low; i <= high; i++ {
		if power[i] < maximum*.003 || power[i] <= power[i-1] || power[i] < power[i+1] {
			continue
		}
		a, b, c := math.Log(max(1e-30, power[i-1])), math.Log(max(1e-30, power[i])), math.Log(max(1e-30, power[i+1]))
		delta := 0.0
		if d := a - 2*b + c; math.Abs(d) > 1e-12 {
			delta = max(-.5, min(.5, .5*(a-c)/d))
		}
		peaks = append(peaks, candidate{frequency: (float64(i) + delta) * float64(rate) / float64(n), power: power[i]})
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].power > peaks[j].power })
	var selected []candidate
	for _, p := range peaks {
		separate := true
		for _, other := range selected {
			if math.Abs(p.frequency-other.frequency) < max(20, 2*float64(rate)/float64(length)) {
				separate = false
				break
			}
		}
		if separate {
			selected = append(selected, p)
		}
		if len(selected) == MaxModes {
			break
		}
	}
	if len(selected) == 0 {
		return model, fmt.Errorf("no stable resonances found; choose a ringing hit")
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].frequency < selected[j].frequency })
	model = Model{Format: Format, License: "user recording", SourceSHA256: sourceSHA256, HitSHA256: recording.Digest(recording.EncodeWAV(pcm, rate)), SourceRate: rate, RootHz: selected[0].frequency}
	model.RootMIDI = max(12, min(95, int(math.Round(69+12*math.Log2(model.RootHz/440)))))
	weightSum, covered := 0.0, 0.0
	for _, p := range selected {
		decay, confidence := fitDecay(pcm, rate, p.frequency)
		weight := math.Sqrt(p.power)
		weightSum += weight
		model.Modes = append(model.Modes, Mode{FrequencyHz: p.frequency, Ratio: p.frequency / model.RootHz, Weight: weight, T60: decay, DecayConfidence: confidence})
	}
	for i := range model.Modes {
		model.Modes[i].Weight /= weightSum
	}
	// Residual broad-band energy colors the short excitation, rather than
	// becoming an unbounded noise tail. Spectral bands are counted once.
	band := max(1, int(2*float64(n)/float64(length)))
	for i, p := range power {
		for _, m := range model.Modes {
			if math.Abs(float64(i)-m.FrequencyHz*float64(n)/float64(rate)) <= float64(band) {
				covered += p
				break
			}
		}
	}
	model.NoiseMix = min(.5, max(0, 1-covered/max(total, 1e-30)))
	return model, nil
}

func fft(x []complex128) {
	for i, j := 1, 0; i < len(x); i++ {
		bit := len(x) >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for length := 2; length <= len(x); length <<= 1 {
		step := cmplx.Rect(1, -2*math.Pi/float64(length))
		for start := 0; start < len(x); start += length {
			phase := complex(1, 0)
			for j := 0; j < length/2; j++ {
				a, b := x[start+j], phase*x[start+j+length/2]
				x[start+j], x[start+j+length/2] = a+b, a-b
				phase *= step
			}
		}
	}
}

func fitDecay(pcm []float32, rate int, frequency float64) (float64, float64) {
	window := max(rate/125, min(rate*30/1000, int(float64(rate)*6/frequency)))
	hop := max(1, window/2)
	start := rate * 5 / 1000
	limit := min(len(pcm), rate*2)
	var times, logs []float64
	maximum := 0.0
	for offset := start; offset+window <= limit; offset += hop {
		var re, im float64
		for i, x := range pcm[offset : offset+window] {
			phase := 2 * math.Pi * frequency * float64(i) / float64(rate)
			sin, cos := math.Sincos(phase)
			weight := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(window-1))
			re += float64(x) * cos * weight
			im += float64(x) * sin * weight
		}
		amplitude := math.Hypot(re, im) / float64(window)
		maximum = max(maximum, amplitude)
		if amplitude < max(1e-7, maximum*.01) {
			continue
		}
		times = append(times, float64(offset+window/2)/float64(rate))
		logs = append(logs, math.Log(amplitude))
	}
	fallback := min(1, max(.02, float64(len(pcm))/float64(rate)))
	if len(times) < 4 {
		return fallback, 0
	}
	var meanTime, meanLog float64
	for i := range times {
		meanTime += times[i]
		meanLog += logs[i]
	}
	meanTime /= float64(len(times))
	meanLog /= float64(len(times))
	var xx, xy, yy float64
	for i := range times {
		x, y := times[i]-meanTime, logs[i]-meanLog
		xx += x * x
		xy += x * y
		yy += y * y
	}
	if xx <= 0 || xy >= 0 || yy <= 0 {
		return fallback, 0
	}
	slope := xy / xx
	confidence := min(1, max(0, xy*xy/(xx*yy)))
	return min(3, max(.01, -math.Log(1000)/slope)), confidence
}
