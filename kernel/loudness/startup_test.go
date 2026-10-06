package loudness

import (
	"math"
	"testing"
)

func TestIntegratedGatesRequireComplete400msWindows(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		m, err := New(rate)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < rate; i++ {
			sample := float32(.1 * math.Sin(2*math.Pi*1000*float64(i)/float64(rate)))
			if !m.ProcessSample(sample, sample) {
				t.Fatal("tone rejected")
			}
			if i+1 < rate*4/10 && !math.IsInf(m.Metrics().IntegratedLUFS, -1) {
				t.Fatalf("rate=%d frames=%d incomplete loudness gate", rate, i+1)
			}
		}
		metrics := m.Metrics()
		if delta := math.Abs(metrics.IntegratedLUFS - metrics.MomentaryLUFS); delta > .03 {
			t.Fatalf("rate=%d startup bias %g LU", rate, delta)
		}
		// 400..1000 ms in 100 ms hops provides seven complete gates.
		if m.integratedN != 7 {
			t.Fatalf("gates=%d; expected seven complete windows", m.integratedN)
		}
	}
}
