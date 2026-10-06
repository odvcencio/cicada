package expressive

import (
	"math"
	"testing"
)

// Correlation is searched over a broad 0.7..1.4-period range. The half-period
// check below separately rejects an octave-up lock masquerading as this period.
func brassMeasuredPitch(x []float64, fs int, target float64) (float64, float64) {
	lo := int(float64(fs) / target * .7)
	hi := int(float64(fs) / target * 1.4)
	corr := func(lag int) float64 {
		var xy, xx, yy float64
		for i := hi + 2; i < len(x); i++ {
			a, c := x[i], x[i-lag]
			xy += a * c
			xx += a * a
			yy += c * c
		}
		return xy / math.Sqrt(xx*yy+1e-30)
	}
	best := lo
	for i := lo + 1; i <= hi; i++ {
		if corr(i) > corr(best) {
			best = i
		}
	}
	a, c, d := corr(best-1), corr(best), corr(best+1)
	frac := .5 * (a - d) / (a - 2*c + d)
	return float64(fs) / (float64(best) + frac), corr(best / 2)
}
func TestBrassCoupledTuningAndPressure(t *testing.T) {
	for _, fs := range []int{44100, 48000, 96000} {
		for _, hz := range []float64{110, 220, 440, 660} {
			for _, pressure := range []float64{.4, .8} {
				b := NewBrass(fs)
				b.SetExpression(Expression{PitchHz: hz, Pressure: pressure, Brightness: .7, Position: .5})
				b.NoteOn(hz, .8)
				for i := 0; i < fs; i++ {
					b.Next()
				}
				x := make([]float64, fs/3)
				var energy float64
				for i := range x {
					x[i] = b.Next()
					energy += x[i] * x[i]
				}
				measured, half := brassMeasuredPitch(x, fs, hz)
				cents := 1200 * math.Log2(measured/hz)
				t.Logf("fs=%d target=%.0f pressure=%.1f measured=%.3f error=%+.2fc rms=%.4f", fs, hz, pressure, measured, cents, math.Sqrt(energy/float64(len(x))))
				if math.Abs(cents) > 25 {
					t.Errorf("coupled lip/bore tuning out of 25-cent research tolerance: %.2f cents", cents)
				}
				if half > .9 {
					t.Errorf("possible octave lock, half-period correlation %.3f", half)
				}
				if energy/float64(len(x)) < 1e-6 {
					t.Error("lip/bore failed to sustain")
				}
			}
		}
	}
}
func TestBrassContinuousPitchAndRetongue(t *testing.T) {
	b := NewBrass(48000)
	b.NoteOn(220, .8)
	for i := 0; i < 48000; i++ {
		b.Next()
	}
	b.SetExpression(Expression{PitchHz: 330, Pressure: .75, Brightness: .65, Position: .5})
	for i := 0; i < 48000; i++ {
		b.Next()
	}
	x := make([]float64, 16000)
	for i := range x {
		x[i] = b.Next()
	}
	hz, _ := brassMeasuredPitch(x, 48000, 330)
	if math.Abs(1200*math.Log2(hz/330)) > 25 {
		t.Fatalf("continuous pitch reached %.3f Hz", hz)
	}
	b.NoteOn(330, .8)
	before := 0.
	for i := 0; i < 96; i++ {
		s := b.Next()
		before += s * s
	}
	for i := 0; i < 24000; i++ {
		b.Next()
	}
	after := 0.
	for i := 0; i < 4096; i++ {
		s := b.Next()
		after += s * s
	}
	if after/4096 < 1e-5 || !finite(before) {
		t.Fatal("retonguing failed to recover")
	}
}
