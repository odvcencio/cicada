package detmath

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"os"
	"testing"
)

// sweep returns n points spread over [lo, hi]: evenly spaced, offset by a
// fixed irrational fraction so the points are not round numbers. The inputs
// must be the same on every target, so the product is rounded explicitly.
func sweep(lo, hi float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		f := (float64(i) + 0.3819660112501051) / float64(n)
		out[i] = lo + float64((hi-lo)*f)
	}
	return out
}

// logSweep returns n points spread over [lo, hi] (both positive) evenly in
// the logarithm. It uses this package's Log2 and Exp2, not math's, so the
// inputs are the same on every target.
func logSweep(lo, hi float64, n int) []float64 {
	out := sweep(Log2(lo), Log2(hi), n)
	for i, e := range out {
		out[i] = Exp2(e)
	}
	return out
}

func points(n int, parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// nearHalfPiMultiples returns, for n values of k spread over [1, 2^20), the
// doubles nearest k*pi/2 and their two neighbours each side: the arguments
// whose reduction loses the most bits.
func nearHalfPiMultiples(n int) []float64 {
	halfPi := refQuo(refPi, refInt(2))
	var out []float64
	for i := 0; i < n; i++ {
		k := int64(Exp2(float64(i) * 20 / float64(n)))
		x, _ := refMul(refInt(k), halfPi).Float64()
		if x >= TrigLimit {
			continue
		}
		a, b := x, x
		out = append(out, x)
		for j := 0; j < 2; j++ {
			a, b = math.Nextafter(a, 0), math.Nextafter(b, math.Inf(1))
			out = append(out, a, -b)
		}
	}
	return out
}

type accuracyCase struct {
	name   string
	f      func(float64) float64
	ref    func(float64) *big.Float
	inputs func(n int) []float64
}

var accuracyCases = []accuracyCase{
	{"Exp", Exp, refExp, func(n int) []float64 {
		return points(n, sweep(-708, 709.7, n), sweep(-1, 1, n), logSweep(1e-300, 1, n/4))
	}},
	{"Exp2", Exp2, refExp2, func(n int) []float64 {
		return points(n, sweep(-1022, 1023.9, n), sweep(-1, 1, n), logSweep(1e-300, 1, n/4))
	}},
	{"Exp10", Exp10, refExp10, func(n int) []float64 {
		return points(n, sweep(-307, 308.2, n), sweep(-1, 1, n), sweep(-6, 1.2, n))
	}},
	{"Log", Log, refLog, func(n int) []float64 {
		return points(n, logSweep(1e-307, 1e308, n), sweep(0.5, 2, n), logSweep(1+1e-15, 1+1e-3, n/4))
	}},
	{"Log2", Log2, refLog2, func(n int) []float64 {
		return points(n, logSweep(1e-307, 1e308, n), sweep(0.5, 2, n))
	}},
	{"Log10", Log10, refLog10, func(n int) []float64 {
		return points(n, logSweep(1e-307, 1e308, n), sweep(0.5, 2, n), sweep(1, 1e6, n/4))
	}},
	{"Sin", Sin, refSin, func(n int) []float64 {
		return points(n, sweep(-7, 7, n), sweep(-TrigLimit, TrigLimit, n), logSweep(1e-12, 1, n/4), nearHalfPiMultiples(n/8))
	}},
	{"Cos", Cos, refCos, func(n int) []float64 {
		return points(n, sweep(-7, 7, n), sweep(-TrigLimit, TrigLimit, n), logSweep(1e-12, 1, n/4), nearHalfPiMultiples(n/8))
	}},
	{"Tan", Tan, refTan, func(n int) []float64 {
		return points(n, sweep(-7, 7, n), sweep(-TrigLimit, TrigLimit, n), logSweep(1e-12, 1, n/4), nearHalfPiMultiples(n/8))
	}},
	{"Tanh", Tanh, refTanh, func(n int) []float64 {
		return points(n, sweep(-23, 23, n), sweep(-1, 1, n), logSweep(1e-10, 0.2, n/4))
	}},
}

// The accuracy sweep has 2,000 points per range by default and 100,000 with
// DETMATH_DENSE=1 (several minutes).
func sweepSize() int {
	if os.Getenv("DETMATH_DENSE") != "" {
		return 100_000
	}
	return 2_000
}

// maxULP is the bound every function must meet: within 1 ULP of the exact
// result is the package's promise; the functions measure well inside it.
const maxULP = 1.0

func TestAccuracy(t *testing.T) {
	n := sweepSize()
	for _, c := range accuracyCases {
		t.Run(c.name, func(t *testing.T) {
			worst, at := 0.0, 0.0
			for _, x := range c.inputs(n) {
				if e := ulpError(c.f(x), c.ref(x)); e > worst {
					worst, at = e, x
				}
			}
			t.Logf("%s: max error %.4f ULP at %v", c.name, worst, at)
			if worst > maxULP {
				t.Fatalf("%s(%v): error %.4f ULP exceeds %.1f", c.name, at, worst, maxULP)
			}
		})
	}
}

func TestPowAccuracy(t *testing.T) {
	n := sweepSize() / 4
	worstSmall, worstLarge := 0.0, 0.0
	var atSmall, atLarge [2]float64
	for _, x := range points(n, logSweep(1e-12, 1e12, n), sweep(0.05, 3, n)) {
		for _, y := range points(n, sweep(-40, 40, 23), sweep(-2.5, 2.5, 13), []float64{0.5, 1.5, 1.0 / 3}) {
			r := refPow(x, y)
			if v, _ := r.Float64(); v == 0 || math.IsInf(v, 0) || math.Abs(v) < 0x1p-1022 {
				continue
			}
			e := ulpError(Pow(x, y), r)
			if math.Abs(y*math.Log(x)) <= 64 {
				if e > worstSmall {
					worstSmall, atSmall = e, [2]float64{x, y}
				}
			} else if e > worstLarge {
				worstLarge, atLarge = e, [2]float64{x, y}
			}
		}
	}
	t.Logf("Pow: max error %.4f ULP for |y log x| <= 64 at %v, %.4f ULP beyond at %v", worstSmall, atSmall, worstLarge, atLarge)
	if worstSmall > maxULP || worstLarge > maxULP {
		t.Fatalf("Pow error exceeds %.1f ULP", maxULP)
	}
}

func TestSincosMatchesSinAndCos(t *testing.T) {
	for _, x := range sweep(-50, 50, 10_000) {
		s, c := Sincos(x)
		if s != Sin(x) || c != Cos(x) {
			t.Fatalf("Sincos(%v) = %v, %v; Sin, Cos = %v, %v", x, s, c, Sin(x), Cos(x))
		}
	}
}

func TestSpecialValues(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	same := func(a, b float64) bool {
		return a == b && math.Signbit(a) == math.Signbit(b) || a != a && b != b
	}
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"Exp(0)", Exp(0), 1}, {"Exp(-Inf)", Exp(-inf), 0}, {"Exp(Inf)", Exp(inf), inf}, {"Exp(NaN)", Exp(nan), nan},
		{"Exp(710)", Exp(710), inf}, {"Exp(-746)", Exp(-746), 0},
		{"Exp2(10)", Exp2(10), 1024}, {"Exp2(-3)", Exp2(-3), 0.125}, {"Exp2(1024)", Exp2(1024), inf},
		{"Exp10(0)", Exp10(0), 1}, {"Exp10(3)", Exp10(3), 1000}, {"Exp10(-2)", Exp10(-2), 0.01}, {"Exp10(22)", Exp10(22), 1e22},
		{"Log(1)", Log(1), 0}, {"Log(0)", Log(0), -inf}, {"Log(-1)", Log(-1), nan}, {"Log(Inf)", Log(inf), inf},
		{"Log2(8)", Log2(8), 3}, {"Log2(0.25)", Log2(0.25), -2}, {"Log2(0x1p-1074)", Log2(0x1p-1074), -1074},
		{"Log10(1000)", Log10(1000), 3}, {"Log10(1e-5)", Log10(1e-5), -5}, {"Log10(1e22)", Log10(1e22), 22},
		{"Pow(2, 0.5)", Pow(2, 0.5), math.Sqrt2}, {"Pow(10, 2)", Pow(10, 2), 100}, {"Pow(3, 4)", Pow(3, 4), 81},
		{"Pow(-2, 3)", Pow(-2, 3), -8}, {"Pow(-2, 0.5)", Pow(-2, 0.5), nan}, {"Pow(0, -1)", Pow(0, -1), inf},
		{"Pow(-0, -3)", Pow(math.Copysign(0, -1), -3), -inf}, {"Pow(-0, 3)", Pow(math.Copysign(0, -1), 3), math.Copysign(0, -1)},
		{"Pow(x, 0)", Pow(nan, 0), 1}, {"Pow(1, NaN)", Pow(1, nan), 1}, {"Pow(0.5, Inf)", Pow(0.5, inf), 0},
		{"Pow(-1, Inf)", Pow(-1, inf), 1}, {"Pow(-Inf, 3)", Pow(-inf, 3), -inf}, {"Pow(Inf, -1)", Pow(inf, -1), 0},
		{"Pow(1e300, 2)", Pow(1e300, 2), inf}, {"Pow(1e-300, 2)", Pow(1e-300, 2), 0},
		{"Sin(-0)", Sin(math.Copysign(0, -1)), math.Copysign(0, -1)}, {"Cos(0)", Cos(0), 1}, {"Sin(Inf)", Sin(inf), nan},
		{"Tan(0)", Tan(0), 0}, {"Tanh(0)", Tanh(0), 0}, {"Tanh(30)", Tanh(30), 1}, {"Tanh(-30)", Tanh(-30), -1}, {"Tanh(NaN)", Tanh(nan), nan},
	} {
		if !same(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// identityDigest pins the bits of every function over a fixed sweep. The
// functions use no operation that differs by target, so CI requires the same
// digest on arm64 (the PCM24 parity job) and with GOAMD64=v3.
const identityDigest = "0ad93a6fe609c723e1fc684a586556a7c85a613d92227cd78df152af53a85745"

func TestIdentityDigest(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	put := func(v float64) {
		binary.LittleEndian.PutUint64(word[:], math.Float64bits(v))
		h.Write(word[:])
	}
	for _, c := range accuracyCases {
		for _, x := range c.inputs(4_000) {
			put(c.f(x))
		}
	}
	for _, x := range logSweep(1e-6, 1e6, 300) {
		for _, y := range sweep(-12, 12, 41) {
			put(Pow(x, y))
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != identityDigest {
		t.Fatalf("detmath identity digest = %s, want %s", got, identityDigest)
	}
}

// outOfRangeDigest pins the bits that the trigonometric functions return at
// and beyond TrigLimit, for infinities and for NaN: NaN on every target.
const outOfRangeDigest = "77fb1dc61506281cd32c9dff3ce98f5e0a82cc2df6df8497a7d6b0d8bf3b6caa"

func TestTrigOutOfRange(t *testing.T) {
	h := sha256.New()
	var word [8]byte
	for _, x := range []float64{TrigLimit, -TrigLimit, math.Nextafter(TrigLimit, 0), 1e10, -1e18, 1e20, 1.45e19, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		s, c := Sincos(x)
		for _, v := range []float64{Sin(x), Cos(x), Tan(x), s, c} {
			binary.LittleEndian.PutUint64(word[:], math.Float64bits(v))
			h.Write(word[:])
		}
		if inRange := x > -TrigLimit && x < TrigLimit; inRange == math.IsNaN(Sin(x)) {
			t.Errorf("Sin(%v) = %v; want NaN only outside the domain", x, Sin(x))
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != outOfRangeDigest {
		t.Fatalf("out-of-range digest = %s, want %s", got, outOfRangeDigest)
	}
}

func BenchmarkFunctions(b *testing.B) {
	for _, c := range []struct {
		name string
		f    func(float64) float64
		g    func(float64) float64
		x    float64
	}{
		{"Exp", Exp, math.Exp, 0.7}, {"Exp10", Exp10, func(x float64) float64 { return math.Pow(10, x) }, -0.35},
		{"Log", Log, math.Log, 3.7}, {"Log10", Log10, math.Log10, 3.7}, {"Sin", Sin, math.Sin, 0.9},
		{"Tan", Tan, math.Tan, 0.9}, {"Tanh", Tanh, math.Tanh, 0.9},
		{"Pow", func(x float64) float64 { return Pow(x, 1.37) }, func(x float64) float64 { return math.Pow(x, 1.37) }, 3.7},
	} {
		b.Run(c.name+"/detmath", func(b *testing.B) {
			s := 0.0
			for i := 0; i < b.N; i++ {
				s += c.f(c.x + float64(i&7)*1e-3)
			}
			sink = s
		})
		b.Run(c.name+"/math", func(b *testing.B) {
			s := 0.0
			for i := 0; i < b.N; i++ {
				s += c.g(c.x + float64(i&7)*1e-3)
			}
			sink = s
		})
	}
}

var sink float64
