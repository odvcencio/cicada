package detmath

import (
	"math"
	"math/big"
)

// The reference functions evaluate in 320-bit binary floating point with
// math/big, far beyond float64, so their rounding to float64 is the correctly
// rounded result for every input the tests use.

const refPrec = 320

func newRef(x float64) *big.Float { return new(big.Float).SetPrec(refPrec).SetFloat64(x) }
func refInt(n int64) *big.Float   { return new(big.Float).SetPrec(refPrec).SetInt64(n) }

func refAdd(a, b *big.Float) *big.Float { return new(big.Float).SetPrec(refPrec).Add(a, b) }
func refSub(a, b *big.Float) *big.Float { return new(big.Float).SetPrec(refPrec).Sub(a, b) }
func refMul(a, b *big.Float) *big.Float { return new(big.Float).SetPrec(refPrec).Mul(a, b) }
func refQuo(a, b *big.Float) *big.Float { return new(big.Float).SetPrec(refPrec).Quo(a, b) }

var refEps = new(big.Float).SetPrec(refPrec).SetMantExp(big.NewFloat(1), -refPrec-8)

func small(term *big.Float) bool {
	return new(big.Float).Abs(term).Cmp(refEps) < 0
}

// refAtanhSeries returns atanh(s) = s + s^3/3 + s^5/5 + ... for |s| < 1/2.
func refAtanhSeries(s *big.Float) *big.Float {
	sum, power, s2 := refAdd(s, refInt(0)), refAdd(s, refInt(0)), refMul(s, s)
	for k := int64(3); ; k += 2 {
		power = refMul(power, s2)
		term := refQuo(power, refInt(k))
		sum = refAdd(sum, term)
		if small(term) {
			return sum
		}
	}
}

// refAtanSeries returns atan(1/n) for an integer n > 1.
func refAtanSeries(n int64) *big.Float {
	x := refQuo(refInt(1), refInt(n))
	sum, power, x2 := refAdd(x, refInt(0)), refAdd(x, refInt(0)), refMul(x, x)
	for k := int64(3); ; k += 2 {
		power = refMul(power, x2)
		term := refQuo(power, refInt(k))
		if k%4 == 3 {
			sum = refSub(sum, term)
		} else {
			sum = refAdd(sum, term)
		}
		if small(term) {
			return sum
		}
	}
}

var (
	refLn2  = refMul(refInt(2), refAtanhSeries(refQuo(refInt(1), refInt(3))))
	refPi   = refSub(refMul(refInt(16), refAtanSeries(5)), refMul(refInt(4), refAtanSeries(239)))
	refLn10 = refBigLog(refInt(10))
)

// refBigLog returns log(x) for x > 0.
func refBigLog(x *big.Float) *big.Float {
	m := new(big.Float).SetPrec(refPrec)
	e := x.MantExp(m) // x = m * 2^e, m in [0.5, 1)
	if m.Cmp(newRef(math.Sqrt2/2)) < 0 {
		m.SetMantExp(m, 1)
		e--
	}
	s := refQuo(refSub(m, refInt(1)), refAdd(m, refInt(1)))
	return refAdd(refMul(refInt(int64(e)), refLn2), refMul(refInt(2), refAtanhSeries(s)))
}

// refBigExp returns exp(x).
func refBigExp(x *big.Float) *big.Float {
	kf, _ := refQuo(x, refLn2).Float64()
	k := int64(math.Round(kf))
	r := refSub(x, refMul(refInt(k), refLn2))
	const halvings = 12
	r.SetMantExp(r, -halvings)
	sum, term := refInt(1), refInt(1)
	for n := int64(1); ; n++ {
		term = refQuo(refMul(term, r), refInt(n))
		sum = refAdd(sum, term)
		if small(term) {
			break
		}
	}
	for i := 0; i < halvings; i++ {
		sum = refMul(sum, sum)
	}
	return sum.SetMantExp(sum, int(k))
}

// refBigSinCos returns sin(x) and cos(x).
func refBigSinCos(x *big.Float) (*big.Float, *big.Float) {
	halfPi := refQuo(refPi, refInt(2))
	kf, _ := refQuo(x, halfPi).Float64()
	k := int64(math.Round(kf))
	r := refSub(x, refMul(refInt(k), halfPi))
	r2 := refMul(r, r)
	sin, cos := refAdd(r, refInt(0)), refInt(1)
	sinTerm, cosTerm := refAdd(r, refInt(0)), refInt(1)
	for n := int64(1); ; n++ {
		sinTerm = refQuo(refMul(sinTerm, r2), refInt(-(2*n)*(2*n+1)))
		cosTerm = refQuo(refMul(cosTerm, r2), refInt(-(2*n-1)*(2*n)))
		sin, cos = refAdd(sin, sinTerm), refAdd(cos, cosTerm)
		if small(sinTerm) && small(cosTerm) {
			break
		}
	}
	switch ((k % 4) + 4) % 4 {
	case 1:
		return cos, new(big.Float).Neg(sin)
	case 2:
		return new(big.Float).Neg(sin), new(big.Float).Neg(cos)
	case 3:
		return new(big.Float).Neg(cos), sin
	}
	return sin, cos
}

// The reference functions of float64 arguments.

func refExp(x float64) *big.Float   { return refBigExp(newRef(x)) }
func refExp2(x float64) *big.Float  { return refBigExp(refMul(newRef(x), refLn2)) }
func refExp10(x float64) *big.Float { return refBigExp(refMul(newRef(x), refLn10)) }
func refLog(x float64) *big.Float   { return refBigLog(newRef(x)) }
func refLog2(x float64) *big.Float  { return refQuo(refBigLog(newRef(x)), refLn2) }
func refLog10(x float64) *big.Float { return refQuo(refBigLog(newRef(x)), refLn10) }
func refPow(x, y float64) *big.Float {
	return refBigExp(refMul(newRef(y), refBigLog(newRef(x))))
}
func refSin(x float64) *big.Float { s, _ := refBigSinCos(newRef(x)); return s }
func refCos(x float64) *big.Float { _, c := refBigSinCos(newRef(x)); return c }
func refTan(x float64) *big.Float { s, c := refBigSinCos(newRef(x)); return refQuo(s, c) }
func refTanh(x float64) *big.Float {
	e := refBigExp(refMul(refInt(2), newRef(x)))
	return refQuo(refSub(e, refInt(1)), refAdd(e, refInt(1)))
}

// ulpError returns |got - ref| in units of the last place of ref rounded to
// float64.
func ulpError(got float64, ref *big.Float) float64 {
	want, _ := ref.Float64()
	if want == 0 {
		if got == 0 {
			return 0
		}
		return math.Inf(1)
	}
	a := math.Abs(want)
	ulp := math.Nextafter(a, math.Inf(1)) - a
	if e := math.Float64bits(a) & (1<<52 - 1); e == 0 && got != want {
		// At a power of two the spacing below is half the spacing above;
		// measure against the smaller one when the error lies below.
		if math.Abs(got) < a {
			ulp = a - math.Nextafter(a, 0)
		}
	}
	// Divide in big precision: near the bottom of the range the difference
	// itself is subnormal and would round on conversion to float64.
	diff := new(big.Float).Abs(refSub(newRef(got), ref))
	r, _ := refQuo(diff, newRef(ulp)).Float64()
	return r
}

// split64 returns the float64 nearest v and the float64 nearest the rest,
// with hi rounded to keep only bits significant bits.
func split64(v *big.Float, bits uint) (hi, lo float64) {
	h := new(big.Float).SetPrec(bits).SetMode(big.ToNearestEven).Set(v)
	hi, _ = h.Float64()
	lo, _ = refSub(v, newRef(hi)).Float64()
	return hi, lo
}
