package analysis

import "math"

// fftPlan holds the tables for one radix-2 complex FFT size.
type fftPlan struct {
	n        int
	cos, sin []float64
	rev      []int
	window   []float64 // periodic Hann
	winSumSq float64
}

func newFFTPlan(n int) *fftPlan {
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
		w := 0.5 - float64(0.5*math.Cos(2*math.Pi*float64(i)/float64(n)))
		p.window[i] = w
		p.winSumSq += float64(w * w)
	}
	return p
}

// transform runs the forward FFT in place.
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
				tr := float64(re[b]*wr) - float64(im[b]*wi)
				ti := float64(re[b]*wi) + float64(im[b]*wr)
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}
