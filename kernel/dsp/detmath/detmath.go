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
//
// Functions named ...Near (TanhNear, SinPiNear) are a second tier for
// per-sample nonlinearities on the audio path only: they give the same bits on
// every target like the rest, but trade the 1 ULP bound for speed, and each
// states its bound and the test that proves it. Coefficient setup, timing,
// tuning and anything whose error accumulates use the exact functions.
// TestRenderPathAvoidsMathLibrary fails on a ...Near call outside the
// per-sample sites it lists, and every processor that uses one is pinned by a
// digest.
package detmath

import "math"

const splitter = 0x1p27 + 1 // Veltkamp's constant for float64

// split returns hi + lo = a exactly, each with at most 26 significant bits.
func split(a float64) (hi, lo float64) {
	if a > 0x1p995 || a < -0x1p995 {
		return splitHuge(a)
	}
	t := float64(splitter * a)
	hi = t - (t - a)
	return hi, a - hi
}

// scale returns math.Ldexp(v, m) for the values the exponential kernels
// produce: v with 0.98 <= |v| < 2.04 (or 0, an infinity or NaN with m == 0),
// and the low part of a double-double whose high part is such a v, with
// m >= 0. Then v*2^m is zero or normal, so multiplying by 2^m (built from its
// bits) is exact and equals Ldexp, without Ldexp's general cases.
func scale(v float64, m int) float64 {
	if m == 0 {
		return v
	}
	if m >= -1020 && m <= 1020 {
		return v * math.Float64frombits(uint64(m+1023)<<52)
	}
	return math.Ldexp(v, m)
}

// splitHuge is split for |a| > 2^995, where splitter*a could overflow: it
// splits a*2^-28 (twice for |a| > 2^1023) and scales back, which is exact.
// It is a separate function so that split, which is not recursive, inlines.
func splitHuge(a float64) (hi, lo float64) {
	a *= 0x1p-28
	scale := 0x1p28
	if a > 0x1p995 || a < -0x1p995 {
		a *= 0x1p-28
		scale = 0x1p56
	}
	t := float64(splitter * a)
	hi = t - (t - a)
	return hi * scale, (a - hi) * scale
}

// twoProd returns p = fl(a*b) and e with p + e = a*b exactly, barring
// overflow and underflow.
func twoProd(a, b float64) (p, e float64) {
	p = float64(a * b)
	// split(a) and split(b), written out: split does not inline, and twoProd
	// runs in every reduction and kernel.
	var ah, al, bh, bl float64
	if a > 0x1p995 || a < -0x1p995 {
		ah, al = splitHuge(a)
	} else {
		t := float64(splitter * a)
		ah = t - (t - a)
		al = a - ah
	}
	if b > 0x1p995 || b < -0x1p995 {
		bh, bl = splitHuge(b)
	} else {
		t := float64(splitter * b)
		bh = t - (t - b)
		bl = b - bh
	}
	e = ((float64(ah*bh) - p) + float64(ah*bl) + float64(al*bh)) + float64(al*bl)
	return p, e
}

// twoProdSplit is twoProd(a, b) for a constant b whose halves bh + bl, as
// split gives them, are constants too: the same bits without splitting b on
// every call. TestConstantSplits checks each pair.
func twoProdSplit(a, b, bh, bl float64) (p, e float64) {
	p = float64(a * b)
	var ah, al float64
	if a > 0x1p995 || a < -0x1p995 {
		ah, al = splitHuge(a)
	} else {
		t := float64(splitter * a)
		ah = t - (t - a)
		al = a - ah
	}
	e = ((float64(ah*bh) - p) + float64(ah*bl) + float64(al*bh)) + float64(al*bl)
	return p, e
}

// The halves split gives for the constants twoProdSplit multiplies by.
const (
	ln2HiH, ln2HiL     = 0x1.62e43p-01, -0x1.05c611p-29
	ln10HiH, ln10HiL   = 0x1.26bb1b8p+01, 0x1.daaa8bp-26
	halfPi4H, halfPi4L = 0x1.b839a28p-104, -0x1.6fdb1f8p-131
	piHiH, piHiL       = 0x1.921fb58p+01, -0x1.dde974p-26
)

// twoSquare returns twoProd(a, a), splitting a once.
func twoSquare(a float64) (p, e float64) {
	p = float64(a * a)
	var ah, al float64
	if a > 0x1p995 || a < -0x1p995 {
		ah, al = splitHuge(a)
	} else {
		t := float64(splitter * a)
		ah = t - (t - a)
		al = a - ah
	}
	e = ((float64(ah*ah) - p) + float64(ah*al) + float64(al*ah)) + float64(al*al)
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
	return scale(hi+lo, m)
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
	rh, rl := twoProdSplit(f, ln2Hi, ln2HiH, ln2HiL)
	rl += float64(f * ln2Lo)
	hi, lo, m := expReduced(int(k), rh, rl)
	return scale(hi+lo, m)
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
	h, l := twoProdSplit(x, ln10Hi, ln10HiH, ln10HiL)
	l += float64(x * ln10Lo)
	h, l = fastTwoSum(h, l)
	hi, lo, m := expDD(h, l)
	return scale(hi+lo, m)
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
	q = 1.0/23 + float64(z*q)
	q = 1.0/21 + float64(z*q)
	q = 1.0/19 + float64(z*q)
	q = 1.0/17 + float64(z*q)
	q = 1.0/15 + float64(z*q)
	q = 1.0/13 + float64(z*q)
	q = 1.0/11 + float64(z*q)
	q = 1.0/9 + float64(z*q)
	q = 1.0/7 + float64(z*q)
	q = 1.0/5 + float64(z*q)
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

// Log1p returns log(1 + x) for x >= -1, accurate where x is small. It is
// NaN below -1 and for NaN, -Inf at -1, and keeps the sign of zero. It is
// FreeBSD's s_log1p.c (as in Go's math.Log1p) in plain float64 with every
// product rounded, at about a sixth of the cost of a double-double logarithm:
// 1 + x = 2^k * (1 + f) with sqrt(2)/2 < 1 + f < sqrt(2), a correction c for
// the rounding of 1 + x, and log(1 + f) from s = f/(2 + f) and a degree-14
// polynomial in s. Measured error: at most 0.80 ULP (TestLog1pAccuracy, dense
// sweep).
func Log1p(x float64) float64 {
	x = float64(x) // see the package comment
	const (
		sqrt2M1     = 4.142135623730950488017e-01  // sqrt(2) - 1
		sqrt2HalfM1 = -2.928932188134524755992e-01 // sqrt(2)/2 - 1
		ln2Hi       = 6.93147180369123816490e-01   // ln2Hi + ln2Lo = ln 2; k*ln2Hi is exact
		ln2Lo       = 1.90821492927058770002e-10
		lp1         = 6.666666666666735130e-01
		lp2         = 3.999999999940941908e-01
		lp3         = 2.857142874366239149e-01
		lp4         = 2.222219843214978396e-01
		lp5         = 1.818357216161805012e-01
		lp6         = 1.531383769920937332e-01
		lp7         = 1.479819860511658591e-01
	)
	switch {
	case x != x || x == math.Inf(1):
		return x
	case x < -1:
		return math.NaN()
	case x == -1:
		return math.Inf(-1)
	}
	a := math.Abs(x)
	var f, c float64
	var iu uint64
	k := 1
	if a < sqrt2M1 {
		if a < 0x1p-29 {
			if a < 0x1p-54 {
				return x // log1p(x) = x - x^2/2 + ..., and x^2/2 is below half an ULP of x
			}
			return x - float64(float64(x*x)*0.5)
		}
		if x > sqrt2HalfM1 {
			k, f, iu = 0, x, 1 // 1 + x is already in range: f = x
		}
	}
	if k != 0 {
		u := x
		if a < 0x1p53 {
			u = 1 + x
			iu = math.Float64bits(u)
			k = int((iu >> 52) - 1023)
			if k > 0 {
				c = 1 - (u - x)
			} else {
				c = x - (u - 1)
			}
			c /= u // the rounding error of 1 + x, relative to it
		} else {
			iu = math.Float64bits(u)
			k = int((iu >> 52) - 1023)
		}
		iu &= 0x000fffffffffffff
		if iu < 0x0006a09e667f3bcd { // below the mantissa of sqrt(2)
			u = math.Float64frombits(iu | 0x3ff0000000000000)
		} else {
			k++
			u = math.Float64frombits(iu | 0x3fe0000000000000)
			iu = (0x0010000000000000 - iu) >> 2
		}
		f = u - 1
	}
	hfsq := float64(float64(0.5*f) * f)
	kf := float64(k)
	if iu == 0 { // |f| < 2^-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			c += float64(kf * ln2Lo)
			return float64(kf*ln2Hi) + c
		}
		r := float64(hfsq * (1 - float64(0.66666666666666666*f)))
		if k == 0 {
			return f - r
		}
		return float64(kf*ln2Hi) - ((r - (float64(kf*ln2Lo) + c)) - f)
	}
	s := f / (2 + f)
	z := float64(s * s)
	p := lp6 + float64(z*lp7)
	p = lp5 + float64(z*p)
	p = lp4 + float64(z*p)
	p = lp3 + float64(z*p)
	p = lp2 + float64(z*p)
	p = lp1 + float64(z*p)
	r := float64(z * p)
	if k == 0 {
		return f - (hfsq - float64(s*(hfsq+r)))
	}
	return float64(kf*ln2Hi) - ((hfsq - (float64(s*(hfsq+r)) + (float64(kf*ln2Lo) + c))) - f)
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
	r := scale(hi+lo, m)
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
		q = -929569.0/638512875 + float64(z*q)
		q = 21844.0/6081075 + float64(z*q)
		q = -1382.0/155925 + float64(z*q)
		q = 62.0/2835 + float64(z*q)
		q = -17.0/315 + float64(z*q)
		q = 2.0/15 + float64(z*q)
		q = -1.0/3 + float64(z*q)
		return x + float64(float64(x*z)*q)
	}
	// tanh(a) = 1 - 2/(e^(2a) + 1).
	eh, el, m := expDD(2*a, 0) // 2a <= 44: no overflow or underflow
	eh, el = scale(eh, m), scale(el, m)
	dh, dl := twoSum(eh, 1)
	dl += el
	qh, ql := divDD(2, 0, dh, dl)
	th, tl := twoSum(1, -qh)
	return math.Copysign(th+(tl-ql), x)
}

// TanhNear returns tanh(x) within 2 ULP (measured: at most 1.77 ULP over a
// dense sweep and the hard cases, TestTanhNearAccuracy), at about 60% of
// Tanh's cost. It is second tier (see the package comment): for |x| >= 0.125
// it returns E/(E+2) with E = e^(2|x|) - 1 in plain float64, where Tanh forms
// the quotient in double-double; below that it is Tanh.
func TanhNear(x float64) float64 {
	x = float64(x) // see the package comment
	a := math.Abs(x)
	switch {
	case x != x:
		return x
	case a < 0.125:
		return Tanh(x)
	case a > 22:
		return math.Copysign(1, x)
	}
	eh, el, m := expDD(2*a, 0) // 2a <= 44: no overflow or underflow
	eh, el = scale(eh, m), scale(el, m)
	e := (eh - 1) + el // eh - 1 is exact for eh in [1, 4) and within an ULP above
	return math.Copysign(e/(e+2), x)
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
	p, pe := twoProdSplit(k, halfPi4, halfPi4H, halfPi4L)
	l := ((e1 + e2) - p) - pe
	rh, rl = twoSum(h, l)
	return rh, rl, int(k - float64(4*math.Floor(float64(k/4))))
}

// sinKernel returns sin(rh + rl) as a double-double for |rh| <= pi/4.
func sinKernel(rh, rl float64) (hi, lo float64) {
	z := float64(rh * rh)
	// sin(r) = r + r^3 (-1/6 + z/120 - ...), through r^19 (next term below 2^-72).
	p := -1.0 / 121645100408832000
	p = 1.0/355687428096000 + float64(z*p)
	p = -1.0/1307674368000 + float64(z*p)
	p = 1.0/6227020800 + float64(z*p)
	p = -1.0/39916800 + float64(z*p)
	p = 1.0/362880 + float64(z*p)
	p = -1.0/5040 + float64(z*p)
	p = 1.0/120 + float64(z*p)
	p = -1.0/6 + float64(z*p)
	// sin(rh + rl) = sin(rh) + rl cos(rh), with cos(rh) = 1 - z/2 to 2^-53 of rl.
	tail := float64(float64(rh*z)*p) + float64(rl*(1-float64(0.5*z)))
	return fastTwoSum(rh, tail)
}

// cosKernel returns cos(rh + rl) as a double-double for |rh| <= pi/4.
func cosKernel(rh, rl float64) (hi, lo float64) {
	z, zl := twoSquare(rh)
	// cos(r) = 1 - z/2 + z^2 (1/24 - z/720 + ...), through r^20.
	q := 1.0 / 2432902008176640000
	q = -1.0/6402373705728000 + float64(z*q)
	q = 1.0/20922789888000 + float64(z*q)
	q = -1.0/87178291200 + float64(z*q)
	q = 1.0/479001600 + float64(z*q)
	q = -1.0/3628800 + float64(z*q)
	q = 1.0/40320 + float64(z*q)
	q = -1.0/720 + float64(z*q)
	q = 1.0/24 + float64(z*q)
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

// Atan returns the arctangent of x, in [-pi/2, pi/2]. The kernel is the Cephes
// rational approximation that Go's math.Atan uses (same constants and
// evaluation order), so for |x| <= 0.66 Atan returns what math.Atan returns on
// amd64 without FMA. Above that, math.Atan reduces x to (x-1)/(x+1) or 1/x and
// adds pi/4 or pi/2, and the rounding of those steps takes its error to 1.08
// ULP; here the reduced argument's rounding is corrected to first order and the
// sum is done in double-double. For y and x > 0, Atan(y/x) is the angle
// math.Atan2(y, x) computes. Measured error: at most 0.83 ULP.
func Atan(x float64) float64 {
	x = float64(x)        // see the package comment
	if x == 0 || x != x { // keeps the sign of zero; NaN stays NaN
		return x
	}
	if x < 0 {
		return -atanPositive(-x)
	}
	return atanPositive(x)
}

const (
	atanPio2Hi = 0x1.921fb54442d18p+00 // pi/2 = atanPio2Hi + atanPio2Lo
	atanPio2Lo = 0x1.1a62633145c07p-54
	atanPio4Hi = 0x1.921fb54442d18p-01 // pi/4 = atanPio4Hi + atanPio4Lo
	atanPio4Lo = 0x1.1a62633145c07p-55
)

func atanPositive(x float64) float64 {
	const tan3pio8 = 2.41421356237309504880 // tan(3*pi/8)
	switch {
	case x <= 0.66:
		hi, lo := atanKernel(x)
		return hi + lo
	case x > 0x1p60: // includes +Inf: 1/x is below half an ULP of pi/2
		return atanPio2Hi + (atanPio2Lo - 1/x)
	case x > tan3pio8:
		// atan(x) = pi/2 - atan(1/x). 1/x = u + d, d = (1 - u*x)/x to first order.
		u := 1 / x
		p, pe := twoProd(u, x)
		d := ((1 - p) - pe) / x // 1 - p is exact: p is within an ULP of 1
		hi, lo := atanKernel(u)
		lo += d / (1 + float64(u*u)) // atan(u+d) = atan(u) + d/(1+u^2) + O(d^2)
		s, e := twoSum(atanPio2Hi, -hi)
		return s + ((e - lo) + atanPio2Lo)
	default:
		// atan(x) = pi/4 + atan(t), t = (x-1)/(x+1). x-1 is exact for x in
		// (0.66, 2.42]; x+1 = den + dene exactly; t + d is the exact quotient
		// to first order.
		num := x - 1
		den, dene := twoSum(x, 1)
		t := num / den
		p, pe := twoProd(t, den)
		d := (((num - p) - pe) - float64(t*dene)) / den // num - p is exact
		hi, lo := atanKernel(t)
		lo += d / (1 + float64(t*t))
		s, e := twoSum(atanPio4Hi, hi)
		return s + ((e + lo) + atanPio4Lo)
	}
}

// atanKernel returns atan(x) = hi + lo for |x| <= 0.66 as x + x*z*P(z)/Q(z),
// z = x^2, with Go's math/atan.go constants and evaluation order.
func atanKernel(x float64) (hi, lo float64) {
	const (
		p0 = -8.750608600031904122785e-01
		p1 = -1.615753718733365076637e+01
		p2 = -7.500855792314704667340e+01
		p3 = -1.228866684490136173410e+02
		p4 = -6.485021904942025371773e+01
		q0 = +2.485846490142306297962e+01
		q1 = +1.650270098316988542046e+02
		q2 = +4.328810604912902668951e+02
		q3 = +4.853903996359136964868e+02
		q4 = +1.945506571482613964425e+02
	)
	z := float64(x * x)
	p := float64(p0*z) + p1
	p = float64(p*z) + p2
	p = float64(p*z) + p3
	p = float64(p*z) + p4
	q := z + q0
	q = float64(q*z) + q1
	q = float64(q*z) + q2
	q = float64(q*z) + q3
	q = float64(q*z) + q4
	w := float64(z*p) / q
	return x, float64(x * w)
}

// SinPi returns sin(pi*x). The reduction x = q/2 + r with |r| <= 1/4 is exact
// (it needs no constant of pi), so SinPi costs less than Sin and is the sine of
// pi times x itself, not of the rounded product math.Pi*x. Use it for phases in
// turns or half turns: sin(2*pi*phase) is SinPi(2*phase). It is NaN for NaN
// and infinities, keeps the sign of zero, and is zero at integers (+0 at even
// ones, -0 at odd ones, as the quadrant gives). Measured error: at most 0.75 ULP
// (TestSinPiAccuracy, dense sweep).
func SinPi(x float64) float64 {
	x = float64(x) // see the package comment
	if x == 0 {
		return x
	}
	if a := math.Abs(x); a >= 0x1p1023 && a <= math.MaxFloat64 {
		return 0 // an even integer, where 2*x would overflow: +0, as for every even integer
	}
	const (
		piHi = 0x1.921fb54442d18p+01 // pi = piHi + piLo to 2^-108
		piLo = 0x1.1a62633145c07p-53
	)
	q := math.Round(float64(2 * x)) // 2*x is exact
	// Exact: q is 0, or q/2 is within 1/4 of x and at least 1/2, so x and q/2
	// are within a factor of two of each other (Sterbenz).
	r := x - float64(0.5*q)
	th, tl := twoProdSplit(r, piHi, piHiH, piHiL)
	th, tl = fastTwoSum(th, tl+float64(r*piLo))
	// The quadrant is q mod 4; q is a whole number, so below 2^52 an integer
	// conversion gives it exactly, and above that the float formula does.
	var quadrant int
	if q > -0x1p52 && q < 0x1p52 {
		quadrant = int(int64(q) & 3)
	} else {
		quadrant = int(q - float64(4*math.Floor(float64(q/4))))
	}
	switch quadrant {
	case 1:
		h, l := cosPiKernel(th, tl)
		return h + l
	case 2:
		h, l := sinPiKernel(th, tl)
		return -(h + l)
	case 3:
		h, l := cosPiKernel(th, tl)
		return -(h + l)
	}
	h, l := sinPiKernel(th, tl)
	return h + l
}

// SinPiNear returns sin(pi*x) within 2 ULP (measured: at most 1.67 ULP over a
// dense sweep and the hard cases, TestSinPiNearAccuracy), at about half of
// SinPi's cost. It is second tier (see the package comment): the same exact
// reduction as SinPi, then pi*r and the series in plain float64 instead of
// double-double.
func SinPiNear(x float64) float64 {
	x = float64(x) // see the package comment
	if x == 0 {
		return x
	}
	if a := math.Abs(x); a >= 0x1p1023 && a <= math.MaxFloat64 {
		return 0 // an even integer, where 2*x would overflow: +0, as SinPi gives
	}
	q := math.Round(float64(2 * x))
	r := x - float64(0.5*q) // exact, as in SinPi
	t := float64(r * math.Pi)
	z := float64(t * t)
	z2 := float64(z * z)
	z4 := float64(z2 * z2)
	var quadrant int
	if q > -0x1p52 && q < 0x1p52 {
		quadrant = int(int64(q) & 3)
	} else {
		quadrant = int(q - float64(4*math.Floor(float64(q/4))))
	}
	if quadrant&1 == 0 {
		a0 := -1.0/6 + float64(z*(1.0/120))
		a1 := -1.0/5040 + float64(z*(1.0/362880))
		a2 := -1.0/39916800 + float64(z*(1.0/6227020800))
		a3 := -1.0/1307674368000 + float64(z*(1.0/355687428096000))
		p := ((a0 + float64(z2*a1)) + float64(z4*(a2+float64(z2*a3)))) + float64(float64(z4*z4)*(-1.0/121645100408832000))
		s := t + float64(float64(t*z)*p)
		if quadrant == 2 {
			return -s
		}
		return s
	}
	a0 := 1.0/24 + float64(z*(-1.0/720))
	a1 := 1.0/40320 + float64(z*(-1.0/3628800))
	a2 := 1.0/479001600 + float64(z*(-1.0/87178291200))
	a3 := 1.0/20922789888000 + float64(z*(-1.0/6402373705728000))
	p := ((a0 + float64(z2*a1)) + float64(z4*(a2+float64(z2*a3)))) + float64(float64(z4*z4)*(1.0/2432902008176640000))
	c := (1 - float64(0.5*z)) + float64(z2*p)
	if quadrant == 3 {
		return -c
	}
	return c
}

// expPlain returns e^y for |y| <= 20 in plain float64: y = k*ln2/64 + r with
// |r| <= ln2/128, e^r from its series to r^4/24 (the next term is below
// 2^-44), times 2^(k/64) from exp2Table. Its relative error is below 2^-44:
// enough for the float32 fast paths, whose margins absorb it and whose tests
// check every input. The result is rounded, so no caller fuses it.
func expPlain(y float64) float64 {
	k := math.Round(float64(y * sixtyFourOverLn2))
	r := (y - float64(k*ln2Over64Hi)) - float64(k*ln2Over64Lo)
	p := 1 + float64(r*(1+float64(r*(0.5+float64(r*(1.0/6+float64(r*(1.0/24))))))))
	ki := int(k)
	return float64(float64(exp2Table[ki&63][0]*p) * math.Float64frombits(uint64((ki>>6)+1023)<<52))
}

// Tanh32 returns float32(Tanh(float64(x))), bit for bit, for every float32 x
// (TestTanh32FastPathExhaustive checks every input of the fast path; the
// others call Tanh), at a fraction of the cost of Tanh. It evaluates tanh(|x|)
// in plain float64: below 0.625 with Cody and Waite's rational form x +
// x^3*P(x^2)/Q(x^2) (Cephes' coefficients, as in Go's math.Tanh; relative
// error near 2^-52), above with 1 - 2/(e^(2|x|)+1) and expPlain (absolute
// error below 2^-45). It rounds that to float32 only when every value within
// a margin (relative 2^-45, absolute 2^-42) rounds to the same float32; then
// Tanh, within 1 ULP, rounds there too. Otherwise, and for tiny, large,
// infinite and NaN inputs, it calls Tanh.
func Tanh32(x float32) float32 {
	v := float64(x)
	a := math.Abs(v)
	if !(a >= 0x1p-12 && a < 9) {
		return float32(Tanh(v))
	}
	var t, margin float64
	if a < 0.625 {
		const (
			p0, p1, p2 = -9.64399179425052238628e-1, -9.92877231001918586564e1, -1.61468768441708447952e3
			q0, q1, q2 = 1.12811678491632931402e2, 2.23548839060100448583e3, 4.84406305325125486048e3
		)
		s := float64(a * a)
		num := float64(float64(float64(p0*s)+p1)*s) + p2
		den := float64(float64(float64((s+q0)*s)+q1)*s) + q2
		t = a + float64(float64(a*s)*num)/den
		margin = float64(t * 0x1p-45)
	} else {
		t = 1 - 2/(expPlain(2*a)+1) // 2a < 18
		margin = 0x1p-42
	}
	if r := float32(t - margin); r == float32(t+margin) {
		if x < 0 {
			return -r
		}
		return r
	}
	return float32(Tanh(v))
}

// sinPiKernel is sinKernel with its polynomial evaluated by Estrin's scheme
// instead of Horner's: the same series, every product rounded, but about half
// the chain of dependent operations. SinPi's error bound is measured with it.
func sinPiKernel(rh, rl float64) (hi, lo float64) {
	z := float64(rh * rh)
	z2 := float64(z * z)
	z4 := float64(z2 * z2)
	a0 := -1.0/6 + float64(z*(1.0/120))
	a1 := -1.0/5040 + float64(z*(1.0/362880))
	a2 := -1.0/39916800 + float64(z*(1.0/6227020800))
	a3 := -1.0/1307674368000 + float64(z*(1.0/355687428096000))
	b0 := a0 + float64(z2*a1)
	b1 := a2 + float64(z2*a3)
	p := (b0 + float64(z4*b1)) + float64(float64(z4*z4)*(-1.0/121645100408832000))
	tail := float64(float64(rh*z)*p) + float64(rl*(1-float64(0.5*z)))
	return fastTwoSum(rh, tail)
}

// cosPiKernel is cosKernel with Estrin's scheme for its polynomial.
func cosPiKernel(rh, rl float64) (hi, lo float64) {
	z, zl := twoSquare(rh)
	z2 := float64(z * z)
	z4 := float64(z2 * z2)
	a0 := 1.0/24 + float64(z*(-1.0/720))
	a1 := 1.0/40320 + float64(z*(-1.0/3628800))
	a2 := 1.0/479001600 + float64(z*(-1.0/87178291200))
	a3 := 1.0/20922789888000 + float64(z*(-1.0/6402373705728000))
	b0 := a0 + float64(z2*a1)
	b1 := a2 + float64(z2*a3)
	q := (b0 + float64(z4*b1)) + float64(float64(z4*z4)*(1.0/2432902008176640000))
	hz := float64(0.5 * z)
	w := 1 - hz
	tail := (((1 - w) - hz) - float64(0.5*zl)) + (float64(z2*q) - float64(rh*rl))
	return fastTwoSum(w, tail)
}

// TanPi32 returns float32(Tan(math.Pi*float64(u))), bit for bit, for every
// float32 u (checked over all 2^32 inputs), at a fraction of the cost: for
// 2^-30 <= |u| <= 0.46 it evaluates tan(pi*|u|) in plain float64 with a Pade
// approximant (error near 2^-42) and rounds that to float32 only when every value
// within a relative 2^-32 of it rounds the same way; otherwise, and outside
// that range, it computes the expression itself.
func TanPi32(u float32) float32 {
	a := float64(u)
	if a < 0 {
		a = -a
	}
	if !(a >= 0x1p-30 && a <= 0.46) {
		return float32(Tan(math.Pi * float64(u)))
	}
	// tan(t) for t = pi*a when a <= 1/4, and cot(t) for t = pi*(1/2 - a) above
	// (1/2 - a is exact), from the Pade [7/6] approximant
	// tan(t) = t*N(z)/D(z), z = t^2, within a relative 2^-42 for |t| <= pi/4.
	t := float64(math.Pi * a)
	above := a > 0.25
	if above {
		t = float64(math.Pi * (0.5 - a))
	}
	z := float64(t * t)
	n := 135135 + float64(z*(-17325+float64(z*(378-z))))
	d := 135135 + float64(z*(-62370+float64(z*(3150+float64(-28*z)))))
	v := float64(t*n) / d
	if above {
		v = d / float64(t*n)
	}
	const margin = 0x1p-32
	if r := float32(float64(v * (1 - margin))); r == float32(float64(v*(1+margin))) {
		if u < 0 {
			return -r
		}
		return r
	}
	return float32(Tan(math.Pi * float64(u)))
}

// OneMinusExpNeg2Pi32 returns float32(1 - Exp(-2*math.Pi*float64(u))), bit for
// bit, for every float32 u (checked over all 2^32 inputs), at a fraction of
// the cost: for 2^-20 <= u <= 0.5 it evaluates e^y, y = -2*pi*u, with
// expPlain (relative error below 2^-44), and rounds 1 - e^y to float32 only
// when every value within a relative 2^-32 of it rounds the same way;
// otherwise, and outside that range, it computes the expression itself.
func OneMinusExpNeg2Pi32(u float32) float32 {
	x := float64(u)
	if !(x >= 0x1p-20 && x <= 0.5) {
		return float32(1 - Exp(-2*math.Pi*float64(u)))
	}
	v := 1 - expPlain(float64(-2*math.Pi*x))
	const margin = 0x1p-32
	if r := float32(float64(v * (1 - margin))); r == float32(float64(v*(1+margin))) {
		return r
	}
	return float32(1 - Exp(-2*math.Pi*float64(u)))
}
