package scoreaudition

import "math"

// fftPlan holds the tables for a radix-2 complex FFT of one size.
type fftPlan struct {
	n        int
	cos, sin []float64
	rev      []int
	window   []float64
}

func newPlan(n int) *fftPlan {
	p := &fftPlan{n: n, cos: make([]float64, n/2), sin: make([]float64, n/2), rev: make([]int, n), window: make([]float64, n)}
	for i := 0; i < n/2; i++ {
		a := -2 * math.Pi * float64(i) / float64(n)
		p.cos[i], p.sin[i] = math.Cos(a), math.Sin(a)
	}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := range p.rev {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		p.rev[i] = r
		// Symmetric Hann window.
		p.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return p
}

func (p *fftPlan) transform(re, im []float64) {
	n := p.n
	for i, j := range p.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				wr, wi := p.cos[k*step], p.sin[k*step]
				a, b := start+k, start+k+half
				tr := re[b]*wr - im[b]*wi
				ti := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

// magFrames computes the Hann-windowed magnitude spectrum of count frames of a
// stereo signal. start(i) gives the first sample of frame i; samples outside
// the signal read as zero. The magnitude combines the channels by power,
// sqrt((|L|^2+|R|^2)/2), so opposite-polarity channels do not cancel and a
// centred signal reads as its mono magnitude. fn receives frames in order with
// n/2+1 magnitudes; it must not keep the slice. The left channel is the real
// part and the right the imaginary part of one complex transform.
func magFrames(x stereo, p *fftPlan, count int, start func(int) int, fn func(i int, mag []float64)) {
	n := p.n
	re, im := make([]float64, n), make([]float64, n)
	mag := make([]float64, n/2+1)
	for i := 0; i < count; i++ {
		s := start(i)
		for k := 0; k < n; k++ {
			j := s + k
			if j >= 0 && j < len(x.L) {
				re[k], im[k] = float64(x.L[j])*p.window[k], float64(x.R[j])*p.window[k]
			} else {
				re[k], im[k] = 0, 0
			}
		}
		p.transform(re, im)
		for k := 0; k <= n/2; k++ {
			m := (n - k) % n
			l := math.Hypot(re[k]+re[m], im[k]-im[m]) / 2
			r := math.Hypot(im[k]+im[m], re[k]-re[m]) / 2
			mag[k] = math.Sqrt((l*l + r*r) / 2)
		}
		fn(i, mag)
	}
}
