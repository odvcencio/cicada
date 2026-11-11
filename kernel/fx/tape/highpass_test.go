package tape

import (
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// tapeHighpassDigest pins the tape echo's output and loop state with the
// in-loop high-pass on, changed, turned off and turned on half way. CI checks
// the same digest on arm64.
const tapeHighpassDigest = "982b9204a15c5bc25063a3a97de166584258d86153de5cb81530d513b0520d56"

func TestTapeHighpassDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []Params{
		{Division: fx.Eighth, Feedback: .7, Wow: .4, Flutter: .2, Saturation: .3, DampHz: 5_000, HighpassHz: 300},
		{Division: fx.FreeDelay, TimeMs: 180, Feedback: .85, Wow: 1, Flutter: .7, Saturation: .9, DampHz: 2_000, HighpassHz: 1_500},
		{Division: fx.QuarterDotted, Feedback: .2, Wow: .05, Flutter: 0, Saturation: 0, DampHz: 14_000},
	}
	for i, p := range settings {
		x, err := New([]int{48_000, 44_100, 96_000}[i], 97_500)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		left, right := bitdigest.NewNoise(uint32(i)+11), bitdigest.NewNoise(uint32(i)+12)
		for frame := 0; frame < 40_000; frame++ {
			if frame == 20_000 {
				if err := x.SetParams(settings[(i+1)%len(settings)]); err != nil {
					t.Fatal(err)
				}
			}
			d.Float32(x.Process(bitdigest.Program(left, right, frame)))
			d.Float64(x.feedbackState.left, x.feedbackState.right, x.feedback, x.damp)
		}
	}
	if got := d.Hex(); got != tapeHighpassDigest {
		t.Fatalf("tape high-pass digest = %s, want %s", got, tapeHighpassDigest)
	}
}

// TestHighpassMatchesDelay: with wow, flutter and saturation at 0 the tape
// echo is the delay's loop, and that holds with the high-pass on too: the two
// share fx.EchoHighpass, so their output is the same bit for bit at a time
// that is a whole number of frames.
func TestHighpassMatchesDelay(t *testing.T) {
	for _, rate := range rates {
		d, err := fx.NewDelay(rate, 120_000)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.SetParams(fx.DelayParams{Division: fx.FreeDelay, TimeMs: 250, Feedback: .8, DampHz: 3_200, Width: 1, Mix: 1, HighpassHz: 400}); err != nil {
			t.Fatal(err)
		}
		d.Reset()
		tp, _ := New(rate, 120_000)
		if err := tp.SetParams(Params{Division: fx.FreeDelay, TimeMs: 250, Feedback: .8, DampHz: 3_200, HighpassHz: 400}); err != nil {
			t.Fatal(err)
		}
		tp.Reset()
		var n noise = 9
		for i := 0; i < 2*rate; i++ {
			var l, r float32
			if i < rate/4 {
				l, r = n.next(), n.next()
			}
			dl, dr := d.Process(l, r)
			tl, tr := tp.Process(l, r)
			if dl != tl || dr != tr {
				t.Fatalf("rate %d frame %d: delay %g %g, tape %g %g", rate, i, dl, dr, tl, tr)
			}
		}
	}
}

func TestRejectsHighpassOutOfRange(t *testing.T) {
	for _, hz := range []float64{-1, 10, 19.9, 2_000.5, 1e9} {
		p := DefaultParams()
		p.HighpassHz = hz
		if p.Validate() == nil {
			t.Errorf("highpass %g Hz was accepted", hz)
		}
	}
	for _, hz := range []float64{0, 20, 300, 2_000} {
		p := DefaultParams()
		p.HighpassHz = hz
		if err := p.Validate(); err != nil {
			t.Errorf("highpass %g Hz: %v", hz, err)
		}
	}
}
