package expressive

import (
	"math"
	"testing"
)

func TestGuitarTuning(t *testing.T) {
	for _, sr := range []int{44100, 48000, 96000} {
		for _, f := range []float64{82.4069, 110, 220, 440, 880} {
			g := NewGuitar(sr)
			g.NoteOn(f, .8)
			x := make([]float64, sr/5)
			for i := 0; i < sr/20; i++ {
				g.Next()
			}
			for i := range x {
				x[i] = g.Next()
			}
			best := 0.
			lag := 0
			want := float64(sr) / f
			for k := int(want * .96); k <= int(want*1.04)+1; k++ {
				var a, b, c float64
				for i := k; i < len(x); i++ {
					a += x[i] * x[i-k]
					b += x[i] * x[i]
					c += x[i-k] * x[i-k]
				}
				v := a / math.Sqrt(b*c)
				if v > best {
					best = v
					lag = k
				}
			}
			cents := 1200 * math.Log2(float64(sr)/float64(lag)/f)
			t.Logf("sr=%d f=%g error=%.2f cents corr=%.5f", sr, f, cents, best)
			if math.Abs(cents) > 35 || best < .85 {
				t.Errorf("tuning: %.2f cents corr %g", cents, best)
			}
		}
	}
}
func TestGuitarDamping(t *testing.T) {
	energy := func(d float64) float64 {
		g := NewGuitar(48000)
		g.NoteOn(110, .8)
		g.SetExpression(Expression{PitchHz: 110, Position: .2, Brightness: .7, Damping: d})
		var sum float64
		for i := 0; i < 48000; i++ {
			x := g.Next()
			if i > 24000 {
				sum += x * x
			}
		}
		return sum
	}
	a, b := energy(0), energy(1)
	if !(a > 10*b) {
		t.Fatalf("palm damping ineffective %g %g", a, b)
	}
}

// Bin-centered drive creates harmonics; compare folded nonharmonic energy to
// naive clipping with equal drive. This is a regression, not an alias-free claim.
func TestAmpAliasReduction(t *testing.T) {
	const n = 8192
	const k = 997
	a := NewAmp(48000)
	y := make([]float64, n)
	z := make([]float64, n)
	for i := 0; i < n+1024; i++ {
		x := .7 * math.Sin(2*math.Pi*k*float64(i)/n)
		v := a.Next(x, 1)
		if i >= 1024 {
			y[i-1024] = v
			z[i-1024] = .7 * math.Tanh(4*math.Tanh(19*x))
		}
	}
	power := func(x []float64, bin int) float64 {
		var re, im float64
		for i, v := range x {
			p := 2 * math.Pi * float64(bin*i) / n
			re += v * math.Cos(p)
			im -= v * math.Sin(p)
		}
		return re*re + im*im
	} // fifth harmonic is above Nyquist and folds to 3207
	alias := n - 5*k
	ratio := power(y, alias) / power(y, k)
	naive := power(z, alias) / power(z, k)
	t.Logf("folded fifth relative power filtered=%g naive=%g reduction=%.1f dB", ratio, naive, 10*math.Log10(naive/ratio))
	if !(ratio < naive*.1) {
		t.Fatalf("insufficient alias reduction %g vs %g", ratio, naive)
	}
}
