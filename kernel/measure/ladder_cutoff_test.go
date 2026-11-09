package measure

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
)

// A ladder's cutoff is its stage corner: without resonance four stages are
// 3 dB down at sqrt(2^(1/4)-1), about 0.435, times the cutoff, and with
// resonance the peak sits at the cutoff. Checking where the filter lands, not
// only that it matches its own equations, caught a factor-of-two error that
// put a 1 kHz setting's -3 dB point at 211 Hz.
func TestGraphLadderCutoffLandsAtTheSetting(t *testing.T) {
	knee := math.Sqrt(math.Pow(2, .25) - 1)
	for _, c := range []struct {
		name string
		op   graph.Op
	}{{"ladder", graph.Ladder}, {"diode", graph.Diode}} {
		for _, fc := range []float64{1000, 5000} {
			gain, err := graphGain(c.op, fc, 0)(knee * fc)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(gain+3.01) > 0.5 {
				t.Errorf("%s at %.0f Hz: %.2f dB at %.0f Hz, want about -3 dB", c.name, fc, gain, knee*fc)
			}
			resonant := graphGain(c.op, fc, .9)
			peak, err := resonant(fc)
			if err != nil {
				t.Fatal(err)
			}
			for _, side := range []float64{.7 * fc, 1.4 * fc} {
				g, err := resonant(side)
				if err != nil {
					t.Fatal(err)
				}
				if g >= peak {
					t.Errorf("%s at %.0f Hz with resonance: %.2f dB at %.0f Hz is not below the %.2f dB peak at the cutoff", c.name, fc, g, side, peak)
				}
			}
		}
	}
}
