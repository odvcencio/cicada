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
	g2, g3, g4 := G*G, G*G*G, G*G*G*G
	sigma := g3*s1 + g2*s2 + G*s3 + s4
	denominator := 1 + k*g4
	u := fastmath.Tanh((input - k*feedback(f.last, savage)) / (denominator))
	var y1, y2, y3, y4 float64
	for pass := 0; pass < 2; pass++ {
		y1 = G*u + s1
		y2 = G*y1 + s2
		y3 = G*y2 + s3
		y4 = G*y3 + s4
		if pass == 0 {
			u = fastmath.Tanh((input - k*feedback(y4, savage) - k*sigma) / (denominator))
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
	first := solveDiode(input, G, k, s1, s2, s3, s4)
	refinedInput := input - k*feedback(first[3], savage)
	y := solveDiode(refinedInput, G, 0, s1, s2, s3, s4)
	for index := range f.state {
		f.state[index] = flush(2*y[index] - f.state[index])
	}
	f.last = flush(y[3])
	return y[3]
}

func solveDiode(input, G, k, s1, s2, s3, s4 float64) [4]float64 {
	a3 := (G / 2) / (1 - G*G/2)
	b3 := ((G/2)*s4 + s3) / (1 - G*G/2)
	a2 := (G / 2) / (1 - G*a3/2)
	b2 := ((G/2)*b3 + s2) / (1 - G*a3/2)
	c := G * a3 * a2
	d := G*(a3*b2+b3) + s4
	y1 := ((G/2)*(input-k*d+b2) + s1) / (1 - G*a2/2 + G*k*c/2)
	y2 := a2*y1 + b2
	y3 := a3*y2 + b3
	y4 := G*y3 + s4
	return [4]float64{y1, y2, y3, y4}
}

func flush(value float64) float64 {
	const threshold = 1.1754943508222875e-38
	if value > -threshold && value < threshold {
		return 0
	}
	return value
}
