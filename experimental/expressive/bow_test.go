package expressive

import (
	"math"
	"testing"
)

func bowEnergy(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v * v
	}
	return math.Sqrt(s / float64(len(x)))
}
func bowRender(b *Bow, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = b.Next()
	}
	return x
}

func TestBowStableAndArticulated(t *testing.T) {
	for _, sr := range []int{22050, 44100, 48000, 96000} {
		b := NewBow(sr)
		b.NoteOn(220, 0.8)
		x := bowRender(b, sr)
		for i, v := range x {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 3 {
				t.Fatalf("sr %d sample %d: %g", sr, i, v)
			}
		}
		energy := bowEnergy(x[sr/2:])
		if energy < 0.005 {
			t.Fatalf("sr %d silent bow: %g", sr, energy)
		}
		b.NoteOff()
		tail := bowRender(b, 3*sr)
		if end := bowEnergy(tail[len(tail)-sr/4:]); end > energy*0.1 {
			t.Errorf("sr %d release %g vs sustain %g", sr, end, energy)
		}
	}
}

func TestBowFundamental(t *testing.T) {
	const sr = 48000
	for _, hz := range []float64{110, 220, 440, 880} {
		b := NewBow(sr)
		b.NoteOn(hz, 0.8)
		bowRender(b, sr/2)
		x := bowRender(b, sr/4)
		// Correlation peak near one expected period checks pitch, not a spectral
		// maximum that can mistake a strong harmonic for the fundamental.
		best, bestLag := -2.0, 0
		for lag := int(sr / hz * 0.90); lag <= int(sr/hz*1.1); lag++ {
			xy, xx, yy := 0.0, 0.0, 0.0
			for i := lag; i < len(x); i++ {
				a, c := x[i], x[i-lag]
				xy += a * c
				xx += a * a
				yy += c * c
			}
			corr := xy / math.Sqrt(xx*yy)
			if corr > best {
				best = corr
				bestLag = lag
			}
		}
		actual := float64(sr) / float64(bestLag)
		cents := 1200 * math.Log2(actual/hz)
		t.Logf("%g Hz -> %.2f Hz, %.1f cents; correlation %.4f", hz, actual, cents, best)
		if math.Abs(cents) > 35 || best < 0.8 {
			t.Errorf("pitch not established")
		}
	}
}

func TestBowContinuousControlsAndInvalidInput(t *testing.T) {
	b := NewBow(48000)
	b.NoteOn(110, 1)
	for i := 0; i < 96000; i++ {
		if i%240 == 0 {
			f := float64(i) / 96000
			b.SetExpression(Expression{PitchHz: 110 * math.Exp2(f*2), Pressure: 0.2 + 0.8*f, Position: f, Brightness: f, Vibrato: 25, Damping: f})
		}
		x := b.Next()
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 3 {
			t.Fatalf("sample %d: %g", i, x)
		}
	}
	b.SetExpression(Expression{PitchHz: math.NaN(), Pressure: math.Inf(1), Position: -100, Brightness: math.NaN()})
	b.NoteOn(math.NaN(), math.Inf(1))
	for i := 0; i < 1000; i++ {
		if x := b.Next(); math.IsNaN(x) || math.IsInf(x, 0) {
			t.Fatalf("invalid input poisoned state")
		}
	}
}

func TestBowPressureRemovesSustainedExcitation(t *testing.T) {
	b := NewBow(48000)
	b.NoteOn(220, 0.8)
	bowRender(b, 48000)
	sustain := bowEnergy(bowRender(b, 12000))
	b.SetExpression(Expression{PitchHz: 220, Pressure: 0, Position: 0.24, Brightness: 0.6, Damping: 0.15})
	bowRender(b, 3*48000)
	withoutBow := bowEnergy(bowRender(b, 12000))
	if withoutBow > sustain*0.1 {
		t.Fatalf("zero pressure failed to stop excitation: %g vs %g", withoutBow, sustain)
	}
	b.SetExpression(Expression{PitchHz: 220, Pressure: 0.6, Position: 0.24, Brightness: 0.6, Damping: 0.15})
	bowRender(b, 48000)
	if resumed := bowEnergy(bowRender(b, 12000)); resumed < sustain*0.2 {
		t.Fatalf("restored pressure did not resume bow: %g vs %g", resumed, sustain)
	}
}

func BenchmarkBow(b *testing.B) {
	v := NewBow(48000)
	v.NoteOn(220, 0.8)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Next()
	}
}
