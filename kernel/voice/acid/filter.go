package acid

import "m31labs.dev/cicada/kernel/dsp/fastmath"

func feedback(value, savage float64) float64 {
	normal := fastmath.Tanh(value)
	if savage == 0 {
		return normal
	}
	aggressive := fastmath.Tanh(1.6*value+.15) - fastmath.Tanh(.15)
	return normal*(1-savage) + aggressive*savage
}

func (f *filterState) processLadder(input, G, g, k, savage float64) float64 {
	inv := 1 / (1 + g)
	s1, s2, s3, s4 := f.state[0]*inv, f.state[1]*inv, f.state[2]*inv, f.state[3]*inv
	g4 := G * G * G * G
	denominator := 1 + k*g4
	u := fastmath.Tanh((input - k*feedback(f.last, savage)) / (denominator))
	var y1, y2, y3, y4 float64
	for pass := 0; pass < 2; pass++ {
		y1 = G*u + s1
		y2 = G*y1 + s2
		y3 = G*y2 + s3
		y4 = G*y3 + s4
		if pass == 0 {
			// The current estimate already contains the saved stage states.
			// Subtracting their sum again overdrives the feedback loop.
			u = fastmath.Tanh((input - k*feedback(y4, savage)) / denominator)
		}
	}
	f.state[0] = flush(2*y1 - f.state[0])
	f.state[1] = flush(2*y2 - f.state[1])
	f.state[2] = flush(2*y3 - f.state[2])
	f.state[3] = flush(2*y4 - f.state[3])
	f.last = flush(y4)
	return y4
}

func (f *filterState) processDiode(input, G, g, k, savage float64) float64 {
	inv := 1 / (1 + g)
	s1, s2, s3, s4 := f.state[0]*inv, f.state[1]*inv, f.state[2]*inv, f.state[3]*inv
	coefficients := prepareDiode(G, s1, s2, s3, s4)
	first := coefficients.solve(input, k)
	refinedInput := input - k*feedback(first[3], savage)
	y := coefficients.solve(refinedInput, 0)
	for index := range f.state {
		f.state[index] = flush(2*y[index] - f.state[index])
	}
	f.last = flush(y[3])
	return y[3]
}

// processDiodeShaped reuses terms that depend only on G across the two
// oversampled phases. The state-dependent terms are still solved in order.
func (f *filterState) processDiodeShaped(input, G, k, savage, inv, a3, a2, invDen3, invDen2, invFirstDen, invRefinedDen float64) float64 {
	s1, s2, s3, s4 := f.state[0]*inv, f.state[1]*inv, f.state[2]*inv, f.state[3]*inv
	b3 := ((G/2)*s4 + s3) * invDen3
	b2 := ((G/2)*b3 + s2) * invDen2
	d := G*(a3*b2+b3) + s4
	y1 := ((G/2)*(input-k*d+b2) + s1) * invFirstDen
	y2 := a2*y1 + b2
	y3 := a3*y2 + b3
	y4 := G*y3 + s4
	refinedInput := input - k*feedback(y4, savage)
	y1 = ((G/2)*(refinedInput+b2) + s1) * invRefinedDen
	y2 = a2*y1 + b2
	y3 = a3*y2 + b3
	y4 = G*y3 + s4
	f.state[0] = flush(2*y1 - f.state[0])
	f.state[1] = flush(2*y2 - f.state[1])
	f.state[2] = flush(2*y3 - f.state[2])
	f.state[3] = flush(2*y4 - f.state[3])
	f.last = flush(y4)
	return y4
}

type diodeCoefficients struct {
	G, s1, s4      float64
	a3, b3, a2, b2 float64
	c, d           float64
}

func prepareDiode(G, s1, s2, s3, s4 float64) diodeCoefficients {
	a3 := (G / 2) / (1 - G*G/2)
	b3 := ((G/2)*s4 + s3) / (1 - G*G/2)
	a2 := (G / 2) / (1 - G*a3/2)
	b2 := ((G/2)*b3 + s2) / (1 - G*a3/2)
	c := G * a3 * a2
	d := G*(a3*b2+b3) + s4
	return diodeCoefficients{G: G, s1: s1, s4: s4, a3: a3, b3: b3, a2: a2, b2: b2, c: c, d: d}
}

func (coefficients diodeCoefficients) solve(input, k float64) [4]float64 {
	y1 := ((coefficients.G/2)*(input-k*coefficients.d+coefficients.b2) + coefficients.s1) / (1 - coefficients.G*coefficients.a2/2 + coefficients.G*k*coefficients.c/2)
	y2 := coefficients.a2*y1 + coefficients.b2
	y3 := coefficients.a3*y2 + coefficients.b3
	y4 := coefficients.G*y3 + coefficients.s4
	return [4]float64{y1, y2, y3, y4}
}

func flush(value float64) float64 {
	const threshold = 1.1754943508222875e-38
	if value > -threshold && value < threshold {
		return 0
	}
	return value
}
