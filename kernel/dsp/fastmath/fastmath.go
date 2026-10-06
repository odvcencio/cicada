// Package fastmath contains bounded pure-Go approximations used in the acid
// voice's sample loop. Its coefficients and evaluation order are fixed.
package fastmath

import "math"

// Log2 evaluates log2(x) for positive normal float64 values. The mantissa
// uses an odd atanh series with |z| <= 1/3, so its absolute error is below
// 3e-8 across the compressor detector range.
func Log2(x float64) float64 {
	bits := math.Float64bits(x)
	exponent := int((bits>>52)&0x7ff) - 1023
	mantissa := math.Float64frombits((bits & ((uint64(1) << 52) - 1)) | (uint64(1023) << 52))
	z := (mantissa - 1) / (mantissa + 1)
	z2 := float64(z * z)
	series := 1.0 / 13
	series = 1.0/11 + float64(z2*series)
	series = 1.0/9 + float64(z2*series)
	series = 1.0/7 + float64(z2*series)
	series = 1.0/5 + float64(z2*series)
	series = 1.0/3 + float64(z2*series)
	series = 1 + float64(z2*series)
	return float64(exponent) + float64(2*z*series*math.Log2E)
}

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
	// Unroll the fixed terms without changing their evaluation order or rounding.
	b := float64(2*t*b1) - b2 + c5
	b2, b1 = b1, b
	b = float64(2*t*b1) - b2 + c4
	b2, b1 = b1, b
	b = float64(2*t*b1) - b2 + c3
	b2, b1 = b1, b
	b = float64(2*t*b1) - b2 + c2
	b2, b1 = b1, b
	b = float64(2*t*b1) - b2 + c1
	b2, b1 = b1, b
	return scale * (float64(t*b1) - b2 + c0)
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
	// Round every polynomial product before addition, including x squared,
	// so native arm64 FMA contraction cannot change the feedback state.
	x2 := float64(x * x)
	numerator := 17325 + float64(x2*(378+x2))
	denominator := 3150 + float64(28*x2)
	denominator = 62370 + float64(x2*denominator)
	return x * (135135 + float64(x2*numerator)) / (135135 + float64(x2*denominator))
}

// TanSmall approximates tan(x) through the acid filter's 0.45*sampleRate
// cutoff limit. Half-angle reduction keeps the polynomial in its accurate
// central range when the envelope opens the filter above 8 kHz.
func TanSmall(x float64) float64 {
	const limit = 0.45 * math.Pi / 2
	if x < -limit {
		x = -limit
	} else if x > limit {
		x = limit
	}
	if x < -0.3 || x > 0.3 {
		half := tanCentral(x * .5)
		return 2 * half / (1 - float64(half*half))
	}
	return tanCentral(x)
}

func tanCentral(x float64) float64 {
	x2 := float64(x * x)
	p := 17.0/315 + float64(x2*(62.0/2835))
	p = 2.0/15 + float64(x2*p)
	p = 1.0/3 + float64(x2*p)
	return x * (1 + float64(x2*p))
}
