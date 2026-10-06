package recording

import (
	"fmt"
	"math"
	"sort"
)

// Options controls automatic slicing. Root is the fallback for unpitched hits;
// detected musical notes get individual roots when AutoPitch is enabled.
type Options struct {
	Root      int  `json:"root"`
	Layers    int  `json:"layers"`
	AutoPitch bool `json:"autoPitch"`
}

func DefaultOptions() Options { return Options{Root: 60, Layers: 3, AutoPitch: true} }

type Hit struct {
	Input        int       `json:"input"`
	Start        int       `json:"start"`
	End          int       `json:"end"`
	Rate         int       `json:"rate"`
	LoudnessDB   float64   `json:"loudnessDB"`
	Peak         float64   `json:"peak"`
	PitchHz      float64   `json:"pitchHz"`
	Confidence   float64   `json:"confidence"`
	Root         int       `json:"root"`
	TuneCents    float64   `json:"tuneCents"`
	SourceSHA256 string    `json:"sourceSHA256"`
	PCM          []float32 `json:"-"`
}

// Analyze finds attacks above the measured noise floor and trims their tails.
// A short rising-energy test separates taps even when a previous tail rings.
func Analyze(inputs []Audio, options Options) ([]Hit, error) {
	if len(inputs) == 0 || len(inputs) > 32 || options.Root < 12 || options.Root > 95 || options.Layers < 1 || options.Layers > 8 {
		return nil, fmt.Errorf("choose 1–32 recordings, a root from 12–95, and 1–8 layers")
	}
	var hits []Hit
	total := 0
	for index, a := range inputs {
		total += len(a.PCM)
		if a.Rate < 8000 || a.Rate > 192000 || len(a.PCM) == 0 || total > MaxFrames {
			return nil, fmt.Errorf("recording exceeds rate or total frame limit")
		}
		for _, x := range a.PCM {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || math.Abs(float64(x)) > 8 {
				return nil, fmt.Errorf("recording contains invalid PCM")
			}
		}
		hop := max(1, a.Rate/500)
		energy := make([]float64, (len(a.PCM)+hop-1)/hop)
		for w := range energy {
			var e float64
			end := min(len(a.PCM), (w+1)*hop)
			for _, x := range a.PCM[w*hop : end] {
				e += float64(x) * float64(x)
			}
			energy[w] = math.Sqrt(e / float64(end-w*hop))
		}
		// Smooth over 10 ms so low notes do not create attacks at each zero
		// crossing. Keep the raw source coordinates for slicing.
		rawEnergy := append([]float64(nil), energy...)
		for w := range energy {
			var sum float64
			from := max(0, w-4)
			for _, e := range rawEnergy[from : w+1] {
				sum += e * e
			}
			energy[w] = math.Sqrt(sum / float64(w-from+1))
		}
		ordered := append([]float64(nil), energy...)
		sort.Float64s(ordered)
		noise := ordered[len(ordered)/5]
		threshold := max(.0008, max(noise*6, ordered[len(ordered)-1]*.018))
		var onsets []int
		last := -a.Rate
		for w, e := range energy {
			frame := w * hop
			previous := 0.0
			if w > 0 {
				previous = energy[w-1]
			}
			if e > threshold && (previous < threshold || e > max(threshold, previous*2.5)) && frame-last >= a.Rate*60/1000 {
				onsets = append(onsets, frame)
				last = frame
			}
		}
		for j, onset := range onsets {
			limit := len(a.PCM)
			if j+1 < len(onsets) {
				limit = onsets[j+1]
			}
			start := max(0, onset-a.Rate*3/1000)
			end := limit
			quiet := 0
			for w := onset / hop; w < len(energy) && w*hop < limit; w++ {
				if energy[w] < max(threshold*.3, noise*2) {
					quiet++
				} else {
					quiet = 0
				}
				if quiet >= 15 && w*hop-onset > a.Rate*30/1000 {
					end = min(limit, (w-quiet+2)*hop+a.Rate*5/1000)
					break
				}
			}
			if end-start < a.Rate/100 {
				continue
			}
			hit := Hit{Input: index, Start: start, End: end, Rate: a.Rate, Root: options.Root, SourceSHA256: a.SHA256, PCM: append([]float32(nil), a.PCM[start:end]...)}
			var dc float64
			for _, x := range hit.PCM {
				dc += float64(x)
			}
			dc /= float64(len(hit.PCM))
			var power float64
			measure := min(len(hit.PCM), a.Rate*40/1000)
			for i, x := range hit.PCM {
				y := float64(x) - dc
				hit.PCM[i] = float32(y)
				hit.Peak = max(hit.Peak, math.Abs(y))
				if i < measure {
					power += y * y
				}
			}
			if hit.Peak < threshold {
				continue
			}
			hit.LoudnessDB = 20 * math.Log10(max(1e-12, math.Sqrt(power/float64(measure))))
			hit.PitchHz, hit.Confidence = estimatePitch(hit.PCM, a.Rate)
			if options.AutoPitch && hit.Confidence >= .8 && hit.PitchHz > 0 {
				midi := 69 + 12*math.Log2(hit.PitchHz/440)
				hit.Root = max(12, min(95, int(math.Round(midi))))
				hit.TuneCents = (float64(hit.Root) - midi) * 100
			}
			fade := min(len(hit.PCM)/2, max(1, a.Rate/2000))
			for i := range hit.PCM {
				gain := .9 / hit.Peak
				if i < fade {
					gain *= float64(i) / float64(fade)
				}
				if len(hit.PCM)-1-i < fade {
					gain *= float64(len(hit.PCM)-1-i) / float64(fade)
				}
				hit.PCM[i] *= float32(gain)
			}
			hits = append(hits, hit)
			if len(hits) > 256 {
				return nil, fmt.Errorf("recording contains more than 256 hits")
			}
		}
	}
	if len(hits) == 0 {
		return nil, fmt.Errorf("no hits detected; record closer to the microphone or drop a WAV with clear attacks")
	}
	return hits, nil
}

// Normalized autocorrelation uses a downsampled, bounded 80 ms window. The
// first strong local maximum avoids octave-down estimates on tonal notes.
func estimatePitch(pcm []float32, rate int) (float64, float64) {
	stride := max(1, rate/12000)
	effective := float64(rate) / float64(stride)
	start := min(len(pcm)/8, rate*4/1000)
	n := min((len(pcm)-start)/stride, int(effective*.08))
	if n < 64 {
		return 0, 0
	}
	x := make([]float64, n)
	var mean float64
	for i := range x {
		for j := 0; j < stride; j++ {
			x[i] += float64(pcm[start+i*stride+j]) / float64(stride)
		}
		mean += x[i]
	}
	mean /= float64(n)
	for i := range x {
		x[i] -= mean
	}
	low, high := max(2, int(effective/2000)), min(n/2, int(effective/60))
	correlation := make([]float64, high+2)
	for lag := low; lag <= high+1; lag++ {
		var dot, a, b float64
		for i := 0; i < n-lag; i++ {
			dot += x[i] * x[i+lag]
			a += x[i] * x[i]
			b += x[i+lag] * x[i+lag]
		}
		if a*b > 1e-20 {
			correlation[lag] = dot / math.Sqrt(a*b)
		}
	}
	best := low
	for lag := low + 1; lag <= high; lag++ {
		if correlation[lag] > correlation[best] {
			best = lag
		}
	}
	for lag := low + 1; lag < high; lag++ {
		if correlation[lag] >= max(.8, correlation[best]*.95) && correlation[lag] >= correlation[lag-1] && correlation[lag] > correlation[lag+1] {
			best = lag
			break
		}
	}
	a, b, c := correlation[max(low, best-1)], correlation[best], correlation[best+1]
	delta := 0.0
	if denominator := a - 2*b + c; math.Abs(denominator) > 1e-12 {
		delta = max(-.5, min(.5, .5*(a-c)/denominator))
	}
	if b < .35 {
		return 0, max(0, b)
	}
	return effective / (float64(best) + delta), min(1, max(0, b))
}
