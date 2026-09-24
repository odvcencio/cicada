// Package fastmath contains bounded pure-Go approximations used in the acid
// voice's sample loop. Its coefficients and evaluation order are fixed.
package fastmath

import "math"

// Exp2 evaluates 2^x over [-32,32]. The fractional part uses a degree-five
// Chebyshev expansion on [-0.5,0.5]; the integer scale is assembled exactly.
func Exp2(x float64) float64 {
	if x != x {
		return 0
	}
	if x < -32 {
		x = -32
	} else if x > 32 {
		x = 32
	}
	n := int(x)
	frac := x - float64(n)
	if frac > 0.5 {
		n++
		frac--
	} else if frac < -0.5 {
		n--
		frac++
	}
	scale := math.Float64frombits(uint64(n+1023) << 52)
	if frac == 0 {
		return scale
	}
	const (
		c0 = 1.03025449180961881
		c1 = 0.351803207837704524
		c2 = 0.0303300103540963054
		c3 = 0.00174756361397709545
		c4 = 0.0000755940398270323055
		c5 = 0.00000261727190733682114
	)
	t := 2 * frac
	b1, b2 := 0.0, 0.0
	for _, c := range [...]float64{c5, c4, c3, c2, c1} {
		b := 2*t*b1 - b2 + c
		b2, b1 = b1, b
	}
	return scale * (t*b1 - b2 + c0)
}

// Tanh is the specified Padé 7/6 approximation. Its input is clamped to
// [-4.97,4.97] so feedback stays bounded even under a transient overload.
func Tanh(x float64) float64 {
	if x != x {
		return 0
	}
	if x < -4.97 {
		x = -4.97
	} else if x > 4.97 {
		x = 4.97
	}
	x2 := x * x
	return x * (135135 + x2*(17325+x2*(378+x2))) /
		(135135 + x2*(62370+x2*(3150+28*x2)))
}

// TanSmall approximates tan(x) for |x| <= 0.3, the coefficient range of
// Cicada's oversampled acid filters at supported rates and cutoffs.
func TanSmall(x float64) float64 {
	if x < -0.3 {
		x = -0.3
	} else if x > 0.3 {
		x = 0.3
	}
	x2 := x * x
	return x * (1 + x2*(1.0/3+x2*(2.0/15+x2*(17.0/315+x2*(62.0/2835)))))
}
