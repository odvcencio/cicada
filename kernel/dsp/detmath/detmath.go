// Package detmath provides elementary functions that return the same bits on
// every target: amd64 (any CPU and any GOAMD64 level), arm64 and WebAssembly.
//
// Go's own math functions do not. math.Exp and math.Log use assembly on amd64
// (math.Exp uses an FMA instruction only when the CPU has one), arm64 has its
// own math.Exp assembly, and the pure-Go functions such as math.Sin and
// math.Pow are compiled with fused multiply-adds on arm64. Values that reach
// audio, filter coefficients, gains or a decision in the render and export
// path therefore use this package.
//
// Every function uses only operations that IEEE 754 defines exactly and Go
// never fuses: addition, subtraction, multiplication and division with every
// product rounded by an explicit float64 conversion before it is added, plus
// math.Frexp, math.Ldexp, math.Round and bit operations. Extra precision comes
// from double-double arithmetic (Dekker's two-product with Veltkamp
// splitting). They cost two to four times as much as the math library (see
// the benchmarks); a few run once per sample (the room's and reverb's
// modulation, the amp's saturation, the drive's fold), where that cost is
// measured and accepted for the same bits everywhere.
//
// Every exported function starts with x = float64(x). The conversion rounds an
// argument that a caller computed as a product, so that if the compiler ever
// inlines the function, that product cannot fuse with the function's first
// addition.
//
// Accuracy, measured against 320-bit math/big references in the tests: every
// function is within 1 unit in the last place (ULP) of the exact result over
// its documented domain; the measured maxima are listed on each function.
package detmath

import "math"

const splitter = 0x1p27 + 1 // Veltkamp's constant for float64

// split returns hi + lo = a exactly, each with at most 26 significant bits.
func split(a float64) (hi, lo float64) {
	if a > 0x1p995 || a < -0x1p995 {
		hi, lo = split(a * 0x1p-28)
		return hi * 0x1p28, lo * 0x1p28
	}
	t := float64(splitter * a)
	hi = t - (t - a)
	return hi, a - hi
}

// twoProd returns p = fl(a*b) and e with p + e = a*b exactly, barring
// overflow and underflow.
func twoProd(a, b float64) (p, e float64) {
	p = float64(a * b)
	ah, al := split(a)
	bh, bl := split(b)
	e = ((float64(ah*bh) - p) + float64(ah*bl) + float64(al*bh)) + float64(al*bl)
	return p, e
}

// twoSum returns s = fl(a+b) and e with s + e = a + b exactly.
func twoSum(a, b float64) (s, e float64) {
	s = a + b
	bb := s - a
	return s, (a - (s - bb)) + (b - bb)
}

// fastTwoSum is twoSum for |a| >= |b|.
func fastTwoSum(a, b float64) (s, e float64) {
	s = a + b
	return s, b - (s - a)
}

// mulDD returns (ah + al) * (bh + bl) as a double-double.
func mulDD(ah, al, bh, bl float64) (hi, lo float64) {
	p, e := twoProd(ah, bh)
	e += float64(ah*bl) + float64(al*bh)
	return fastTwoSum(p, e)
}

// divDD returns (ah + al) / (bh + bl) as a double-double.
func divDD(ah, al, bh, bl float64) (hi, lo float64) {
	q := ah / bh
	p, e := twoProd(q, bh)
	r := ((ah - p) - e + al) - float64(q*bl)
	return fastTwoSum(q, r/bh)
}

const (
	ln2Over64Hi      = 0x1.62e42fefap-07 // 36 bits: k*ln2Over64Hi is exact for |k| < 2^17
	ln2Over64Lo      = 0x1.cf79abc9e3b3ap-46
	sixtyFourOverLn2 = 0x1.71547652b82fep+06
	ln2ExpHi         = 0x1.62e42fefa38p-01 // 42 bits: e*ln2ExpHi is exact for |e| < 2^11
	ln2ExpLo         = 0x1.ef35793c7673p-45
	ln2Hi, ln2Lo     = 0x1.62e42fefa39efp-01, 0x1.abc9e3b39803fp-56
	ln10Hi, ln10Lo   = 0x1.26bb1bbb55516p+01, -0x1.f48ad494ea3e9p-53
	invLn2Hi         = 0x1.71547652b82fep+00
	invLn2Lo         = 0x1.777d0ffda0d24p-56
	invLn10Hi        = 0x1.bcb7b1526e50ep-02
	invLn10Lo        = 0x1.95355baaafad3p-57
)

// expReduced returns exp(rh + rl) * 2^(j/64) as a double-double hi + lo and
// the remaining power of two m, so that the result is (hi + lo) * 2^m, where
// k = 64m + j and |rh + rl| <= ln2/128 (plus rounding). Callers add hi and lo
// before scaling: scaling lo alone into the subnormal range would round it.
func expReduced(k int, rh, rl float64) (hi, lo float64, m int) {
	j := k & 63
	m = k >> 6
	r := rh + rl
	// exp(r) - 1 = r + r^2/2 + ... + r^6/720; the next term is below 2^-65.
	p := 1.0 / 720
	p = 1.0/120 + float64(r*p)
	p = 1.0/24 + float64(r*p)
	p = 1.0/6 + float64(r*p)
	p = 0.5 + float64(r*p)
	e := rh + (rl + float64(float64(rh*rh)*p))
	t := exp2Table[j]
	tail := t[1] + float64(t[0]*e) + float64(t[1]*e)
	hi, lo = fastTwoSum(t[0], tail)
	return hi, lo, m
}

// expDD returns exp(xh + xl) as (hi + lo) * 2^m, with |xl| <= ulp(xh).
func expDD(xh, xl float64) (hi, lo float64, m int) {
	switch {
	case xh != xh:
		return xh, 0, 0
	case xh > 709.8:
		return math.Inf(1), 0, 0
	case xh < -745.2:
		return 0, 0, 0
	}
	k := math.Round(float64(xh * sixtyFourOverLn2))
	// k*ln2Over64Hi is exact and close to xh, so the subtraction is exact.
	r := xh - float64(k*ln2Over64Hi)
	rh, rl := fastTwoSum(r, xl-float64(k*ln2Over64Lo))
	return expReduced(int(k), rh, rl)
}

// Exp returns e^x. Measured error: at most 0.51 ULP for results of at least
// 2^-1022 (smaller results are subnormal and rounded twice).
func Exp(x float64) float64 {
	x = float64(x) // see the package comment
	hi, lo, m := expDD(x, 0)
	return math.Ldexp(hi+lo, m)
}

// Exp2 returns 2^x, exact for integers. Measured error: at most 0.51 ULP.
func Exp2(x float64) float64 {
	x = float64(x) // see the package comment
	switch {
	case x != x:
		return x
	case x > 1024:
		return math.Inf(1)
	case x < -1075:
		return 0
	}
	k := math.Round(x * 64) // exact
	f := x - float64(k/64)  // exact: |f| <= 1/128
	rh, rl := twoProd(f, ln2Hi)
	rl += float64(f * ln2Lo)
	hi, lo, m := expReduced(int(k), rh, rl)
	return math.Ldexp(hi+lo, m)
}

// Exp10 returns 10^x, exact where 10^x is a representable integer. Measured
// error: at most 0.51 ULP. Pow(10, x) uses it.
func Exp10(x float64) float64 {
	x = float64(x) // see the package comment
	if x != x {
		return x
	}
	if x > 309 {
		return math.Inf(1)
	}
	if x < -324 {
		return 0
	}
	h, l := twoProd(x, ln10Hi)
	l += float64(x * ln10Lo)
	h, l = fastTwoSum(h, l)
	hi, lo, m := expDD(h, l)
	return math.Ldexp(hi+lo, m)
}

// logDD returns log(x) as a double-double for finite x > 0.
func logDD(x float64) (hi, lo float64) {
	m, e := math.Frexp(x) // x = m * 2^e, m in [0.5, 1); Frexp handles subnormals
	if m < math.Sqrt2/2 {
		m *= 2
		e--
	}
	// log(m) = 2 atanh(s), s = (m-1)/(m+1), |s| <= 0.1716.
	n := m - 1 // exact
	dh, dl := twoSum(m, 1)
	sh, sl := divDD(n, 0, dh, dl)
	// log(m) = 2s + 2s^3/3 + 2s z^2 q with z = s^2 and
	// q = 1/5 + z/7 + ... + z^10/25; the next term is below 2^-62 of 2s. The
	// 2s^3/3 term, up to 2% of the result, is kept as a double-double: rounded
	// to a double it limits the accuracy of Pow for large exponents.
	z, zl := twoProd(sh, sh)
	q := 1.0 / 25
	for _, c := range [...]float64{1.0 / 23, 1.0 / 21, 1.0 / 19, 1.0 / 17, 1.0 / 15, 1.0 / 13, 1.0 / 11, 1.0 / 9, 1.0 / 7, 1.0 / 5} {
		q = c + float64(z*q)
	}
	s2 := float64(2 * sh) // exact
	// 2s^3 = 2sh*(z + zl) + 6 sh^2 sl, to 2^-100 of 2s^3.
	ch, cl := twoProd(s2, z)
	cl += float64(s2*zl) + float64(float64(6*z)*sl)
	th, tl := divDD(ch, cl, 3, 0)
	rest := float64(float64(s2*float64(z*z)) * q)
	fe := float64(e)
	hi, lo = twoSum(float64(fe*ln2ExpHi), s2)
	hi, l2 := twoSum(hi, th)
	lo += l2 + float64(fe*ln2ExpLo) + float64(2*sl) + tl + rest
	return fastTwoSum(hi, lo)
}

// Log returns the natural logarithm of x. Measured error: at most 0.51 ULP.
func Log(x float64) float64 {
	x = float64(x) // see the package comment
	switch {
	case x != x || x == math.Inf(1):
		return x
	case x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	}
	hi, lo := logDD(x)
	return hi + lo
}

// Log2 returns the base-2 logarithm of x, exact for powers of two. Measured
// error: at most 0.51 ULP.
func Log2(x float64) float64 {
	x = float64(x) // see the package comment
	if x != x || x <= 0 || x == math.Inf(1) {
		return Log(x)
	}
	if m, e := math.Frexp(x); m == 0.5 {
		return float64(e - 1)
	}
	hi, lo := logDD(x)
	hi, lo = mulDD(hi, lo, invLn2Hi, invLn2Lo)
	return hi + lo
}

// Log10 returns the base-10 logarithm of x, exact for 10^n with integer n
// from 0 to 22. Measured error: at most 0.51 ULP.
func Log10(x float64) float64 {
	x = float64(x) // see the package comment
	if x != x || x <= 0 || x == math.Inf(1) {
		return Log(x)
	}
	hi, lo := logDD(x)
	hi, lo = mulDD(hi, lo, invLn10Hi, invLn10Lo)
	return hi + lo
}

// Pow returns x^y. Special cases follow math.Pow. Measured error: at most
// 0.52 ULP for x from 1e-12 to 1e12 and |y| up to 40.
func Pow(x, y float64) float64 {
	x, y = float64(x), float64(y) // see the package comment
	switch {
	case y == 0 || x == 1:
		return 1
	case x != x || y != y:
		return math.NaN()
	case x == 10:
		return Exp10(y)
	case x == 2:
		return Exp2(y)
	case y == 1:
		return x
	}
	yInt := y == math.Trunc(y) && !math.IsInf(y, 0)
	odd := yInt && math.Abs(y) < 0x1p53 && int64(y)&1 == 1
	switch {
	case x == 0:
		if y < 0 {
			if odd {
				return math.Copysign(math.Inf(1), x)
			}
			return math.Inf(1)
		}
		if odd {
			return x
		}
		return 0
	case math.IsInf(y, 0):
		switch {
		case x == -1:
			return 1
		case (math.Abs(x) < 1) == (y > 0):
			return 0
		}
		return math.Inf(1)
	case math.IsInf(x, 0):
		if math.IsInf(x, -1) {
			return Pow(1/x, -y) // -0 or -Inf, with the sign rule below
		}
		if y < 0 {
			return 0
		}
		return math.Inf(1)
	case x < 0 && !yInt:
		return math.NaN()
	}
	lh, ll := logDD(math.Abs(x))
	if a := float64(y * lh); a > 710 || a < -746 {
		// Far outside the float64 range; the two-product would overflow.
		r := 0.0
		if a > 0 {
			r = math.Inf(1)
		}
		if x < 0 && odd {
			return -r
		}
		return r
	}
	h, l := twoProd(y, lh)
	l += float64(y * ll)
	h, l = fastTwoSum(h, l)
	hi, lo, m := expDD(h, l)
	r := math.Ldexp(hi+lo, m)
	if x < 0 && odd {
		return -r
	}
	return r
}

// Tanh returns the hyperbolic tangent of x. Measured error: at most 0.52 ULP.
func Tanh(x float64) float64 {
	x = float64(x) // see the package comment
	a := math.Abs(x)
	switch {
	case x != x:
		return x
	case a < 0x1p-27:
		return x
	case a > 22:
		return math.Copysign(1, x)
	case a < 0.125:
		// Odd Taylor series; the next term is below 2^-70 of x.
		z := float64(x * x)
		q := 6404582.0 / 10854718875
		for _, c := range [...]float64{-929569.0 / 638512875, 21844.0 / 6081075, -1382.0 / 155925, 62.0 / 2835, -17.0 / 315, 2.0 / 15, -1.0 / 3} {
			q = c + float64(z*q)
		}
		return x + float64(float64(x*z)*q)
	}
	// tanh(a) = 1 - 2/(e^(2a) + 1).
	eh, el, m := expDD(2*a, 0) // 2a <= 44: no overflow or underflow
	eh, el = math.Ldexp(eh, m), math.Ldexp(el, m)
	dh, dl := twoSum(eh, 1)
	dl += el
	qh, ql := divDD(2, 0, dh, dl)
	th, tl := twoSum(1, -qh)
	return math.Copysign(th+(tl-ql), x)
}

const (
	// pi/2 = halfPi1 + halfPi2 + halfPi3 + halfPi4 to 2^-152. The first three
	// have 33 significant bits, so k*halfPi1, k*halfPi2 and k*halfPi3 are exact
	// for |k| < 2^20.
	halfPi1   = 0x1.921fb544p+00
	halfPi2   = 0x1.0b4611a6p-34
	halfPi3   = 0x1.3198a2ep-69
	halfPi4   = 0x1.b839a252049c1p-104
	twoOverPi = 0x1.45f306dc9c883p-01

	// TrigLimit bounds the argument of Sin, Cos, Sincos and Tan: they return
	// NaN for |x| >= TrigLimit (2^20*pi/2, about 1.647e6). Inside it the
	// reduction modulo pi/2 is exact to about 2^-130, which keeps every result
	// within 1 ULP even next to a multiple of pi/2.
	TrigLimit = 0x1p20 * halfPi1
)

// reduce returns r = x - k*pi/2 as rh + rl with |r| <= pi/4 (plus rounding),
// and k mod 4, for |x| < TrigLimit. The quadrant comes from k - 4*floor(k/4)
// in float64, never from converting a large k to an integer.
func reduce(x float64) (rh, rl float64, quadrant int) {
	if x > -math.Pi/4 && x < math.Pi/4 {
		return x, 0, 0
	}
	k := math.Round(float64(x * twoOverPi))
	t := x - float64(k*halfPi1) // exact
	h, e1 := twoSum(t, -float64(k*halfPi2))
	h, e2 := twoSum(h, -float64(k*halfPi3))
	p, pe := twoProd(k, halfPi4)
	l := ((e1 + e2) - p) - pe
	rh, rl = twoSum(h, l)
	return rh, rl, int(k - float64(4*math.Floor(float64(k/4))))
}

// sinKernel returns sin(rh + rl) as a double-double for |rh| <= pi/4.
func sinKernel(rh, rl float64) (hi, lo float64) {
	z := float64(rh * rh)
	// sin(r) = r + r^3 (-1/6 + z/120 - ...), through r^19 (next term below 2^-72).
	p := -1.0 / 121645100408832000
	for _, c := range [...]float64{1.0 / 355687428096000, -1.0 / 1307674368000, 1.0 / 6227020800, -1.0 / 39916800, 1.0 / 362880, -1.0 / 5040, 1.0 / 120, -1.0 / 6} {
		p = c + float64(z*p)
	}
	// sin(rh + rl) = sin(rh) + rl cos(rh), with cos(rh) = 1 - z/2 to 2^-53 of rl.
	tail := float64(float64(rh*z)*p) + float64(rl*(1-float64(0.5*z)))
	return fastTwoSum(rh, tail)
}

// cosKernel returns cos(rh + rl) as a double-double for |rh| <= pi/4.
func cosKernel(rh, rl float64) (hi, lo float64) {
	z, zl := twoProd(rh, rh)
	// cos(r) = 1 - z/2 + z^2 (1/24 - z/720 + ...), through r^20.
	q := 1.0 / 2432902008176640000
	for _, c := range [...]float64{-1.0 / 6402373705728000, 1.0 / 20922789888000, -1.0 / 87178291200, 1.0 / 479001600, -1.0 / 3628800, 1.0 / 40320, -1.0 / 720, 1.0 / 24} {
		q = c + float64(z*q)
	}
	hz := float64(0.5 * z)
	w := 1 - hz
	// cos(rh + rl) = cos(rh) - rl sin(rh), with sin(rh) = rh to 2^-55 of rl.
	tail := (((1 - w) - hz) - float64(0.5*zl)) + (float64(float64(z*z)*q) - float64(rh*rl))
	return fastTwoSum(w, tail)
}

// Sincos returns Sin(x) and Cos(x). See Sin and Cos for the domain.
func Sincos(x float64) (sin, cos float64) {
	x = float64(x)                          // see the package comment
	if !(x > -TrigLimit && x < TrigLimit) { // NaN, infinite or out of range
		return math.NaN(), math.NaN()
	}
	if x > -0x1p-27 && x < 0x1p-27 {
		return x, 1
	}
	rh, rl, q := reduce(x)
	sh, sl := sinKernel(rh, rl)
	ch, cl := cosKernel(rh, rl)
	s, c := sh+sl, ch+cl
	switch q {
	case 1:
		return c, -s
	case 2:
		return -s, -c
	case 3:
		return -c, s
	}
	return s, c
}

// Sin returns the sine of x for |x| < TrigLimit and NaN otherwise. Measured
// error: at most 0.75 ULP, including arguments next to multiples of pi/2.
//
// It evaluates only the kernel its quadrant needs, so it returns exactly the
// sine Sincos returns at about half the cost.
func Sin(x float64) float64 {
	x = float64(x)                          // see the package comment
	if !(x > -TrigLimit && x < TrigLimit) { // NaN, infinite or out of range
		return math.NaN()
	}
	if x > -0x1p-27 && x < 0x1p-27 {
		return x
	}
	rh, rl, q := reduce(x)
	switch q {
	case 1:
		h, l := cosKernel(rh, rl)
		return h + l
	case 2:
		h, l := sinKernel(rh, rl)
		return -(h + l)
	case 3:
		h, l := cosKernel(rh, rl)
		return -(h + l)
	}
	h, l := sinKernel(rh, rl)
	return h + l
}

// Cos returns the cosine of x for |x| < TrigLimit and NaN otherwise. Measured
// error: at most 0.75 ULP, including arguments next to multiples of pi/2.
//
// Like Sin, it evaluates only the kernel its quadrant needs and returns
// exactly the cosine Sincos returns.
func Cos(x float64) float64 {
	x = float64(x)                          // see the package comment
	if !(x > -TrigLimit && x < TrigLimit) { // NaN, infinite or out of range
		return math.NaN()
	}
	if x > -0x1p-27 && x < 0x1p-27 {
		return 1
	}
	rh, rl, q := reduce(x)
	switch q {
	case 1:
		h, l := sinKernel(rh, rl)
		return -(h + l)
	case 2:
		h, l := cosKernel(rh, rl)
		return -(h + l)
	case 3:
		h, l := sinKernel(rh, rl)
		return h + l
	}
	h, l := cosKernel(rh, rl)
	return h + l
}

// Tan returns the tangent of x for |x| < TrigLimit and NaN otherwise.
// Measured error: at most 0.85 ULP, including arguments next to multiples of
// pi/2.
func Tan(x float64) float64 {
	x = float64(x)                          // see the package comment
	if !(x > -TrigLimit && x < TrigLimit) { // NaN, infinite or out of range
		return math.NaN()
	}
	if x > -0x1p-27 && x < 0x1p-27 {
		return x
	}
	rh, rl, q := reduce(x)
	sh, sl := sinKernel(rh, rl)
	ch, cl := cosKernel(rh, rl)
	if q&1 == 1 {
		th, tl := divDD(ch, cl, sh, sl)
		return -(th + tl)
	}
	th, tl := divDD(sh, sl, ch, cl)
	return th + tl
}

var exp2Table = [64][2]float64{ // 2^(j/64) as hi + lo
	{0x1p+00, 0x0p+00},
	{0x1.02c9a3e778061p+00, -0x1.19083535b085dp-56},
	{0x1.059b0d3158574p+00, 0x1.d73e2a475b465p-55},
	{0x1.0874518759bc8p+00, 0x1.186be4bb284ffp-57},
	{0x1.0b5586cf9890fp+00, 0x1.8a62e4adc610bp-54},
	{0x1.0e3ec32d3d1a2p+00, 0x1.03a1727c57b53p-59},
	{0x1.11301d0125b51p+00, -0x1.6c51039449b3ap-54},
	{0x1.1429aaea92dep+00, -0x1.32fbf9af1369ep-54},
	{0x1.172b83c7d517bp+00, -0x1.19041b9d78a76p-55},
	{0x1.1a35beb6fcb75p+00, 0x1.e5b4c7b4968e4p-55},
	{0x1.1d4873168b9aap+00, 0x1.e016e00a2643cp-54},
	{0x1.2063b88628cd6p+00, 0x1.dc775814a8495p-55},
	{0x1.2387a6e756238p+00, 0x1.9b07eb6c70573p-54},
	{0x1.26b4565e27cddp+00, 0x1.2bd339940e9d9p-55},
	{0x1.29e9df51fdee1p+00, 0x1.612e8afad1255p-55},
	{0x1.2d285a6e4030bp+00, 0x1.0024754db41d5p-54},
	{0x1.306fe0a31b715p+00, 0x1.6f46ad23182e4p-55},
	{0x1.33c08b26416ffp+00, 0x1.32721843659a6p-54},
	{0x1.371a7373aa9cbp+00, -0x1.63aeabf42eae2p-54},
	{0x1.3a7db34e59ff7p+00, -0x1.5e436d661f5e3p-56},
	{0x1.3dea64c123422p+00, 0x1.ada0911f09ebcp-55},
	{0x1.4160a21f72e2ap+00, -0x1.ef3691c309278p-58},
	{0x1.44e086061892dp+00, 0x1.89b7a04ef80dp-59},
	{0x1.486a2b5c13cdp+00, 0x1.3c1a3b69062fp-56},
	{0x1.4bfdad5362a27p+00, 0x1.d4397afec42e2p-56},
	{0x1.4f9b2769d2ca7p+00, -0x1.4b309d25957e3p-54},
	{0x1.5342b569d4f82p+00, -0x1.07abe1db13cadp-55},
	{0x1.56f4736b527dap+00, 0x1.9bb2c011d93adp-54},
	{0x1.5ab07dd485429p+00, 0x1.6324c054647adp-54},
	{0x1.5e76f15ad2148p+00, 0x1.ba6f93080e65ep-54},
	{0x1.6247eb03a5585p+00, -0x1.383c17e40b497p-54},
	{0x1.6623882552225p+00, -0x1.bb60987591c34p-54},
	{0x1.6a09e667f3bcdp+00, -0x1.bdd3413b26456p-54},
	{0x1.6dfb23c651a2fp+00, -0x1.bbe3a683c88abp-57},
	{0x1.71f75e8ec5f74p+00, -0x1.16e4786887a99p-55},
	{0x1.75feb564267c9p+00, -0x1.0245957316dd3p-54},
	{0x1.7a11473eb0187p+00, -0x1.41577ee04992fp-55},
	{0x1.7e2f336cf4e62p+00, 0x1.05d02ba15797ep-56},
	{0x1.82589994cce13p+00, -0x1.d4c1dd41532d8p-54},
	{0x1.868d99b4492edp+00, -0x1.fc6f89bd4f6bap-54},
	{0x1.8ace5422aa0dbp+00, 0x1.6e9f156864b27p-54},
	{0x1.8f1ae99157736p+00, 0x1.5cc13a2e3976cp-55},
	{0x1.93737b0cdc5e5p+00, -0x1.75fc781b57ebcp-57},
	{0x1.97d829fde4e5p+00, -0x1.d185b7c1b85d1p-54},
	{0x1.9c49182a3f09p+00, 0x1.c7c46b071f2bep-56},
	{0x1.a0c667b5de565p+00, -0x1.359495d1cd533p-54},
	{0x1.a5503b23e255dp+00, -0x1.d2f6edb8d41e1p-54},
	{0x1.a9e6b5579fdbfp+00, 0x1.0fac90ef7fd31p-54},
	{0x1.ae89f995ad3adp+00, 0x1.7a1cd345dcc81p-54},
	{0x1.b33a2b84f15fbp+00, -0x1.2805e3084d708p-57},
	{0x1.b7f76f2fb5e47p+00, -0x1.5584f7e54ac3bp-56},
	{0x1.bcc1e904bc1d2p+00, 0x1.23dd07a2d9e84p-55},
	{0x1.c199bdd85529cp+00, 0x1.11065895048ddp-55},
	{0x1.c67f12e57d14bp+00, 0x1.2884dff483cadp-54},
	{0x1.cb720dcef9069p+00, 0x1.503cbd1e949dbp-56},
	{0x1.d072d4a07897cp+00, -0x1.cbc3743797a9cp-54},
	{0x1.d5818dcfba487p+00, 0x1.2ed02d75b3707p-55},
	{0x1.da9e603db3285p+00, 0x1.c2300696db532p-54},
	{0x1.dfc97337b9b5fp+00, -0x1.1a5cd4f184b5cp-54},
	{0x1.e502ee78b3ff6p+00, 0x1.39e8980a9cc8fp-55},
	{0x1.ea4afa2a490dap+00, -0x1.e9c23179c2893p-54},
	{0x1.efa1bee615a27p+00, 0x1.dc7f486a4b6bp-54},
	{0x1.f50765b6e454p+00, 0x1.9d3e12dd8a18bp-54},
	{0x1.fa7c1819e90d8p+00, 0x1.74853f3a5931ep-55},
}
