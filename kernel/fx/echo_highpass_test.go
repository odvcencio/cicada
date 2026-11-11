package fx

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// echoHighpassDigest pins the delay's output and loop state with the in-loop
// high-pass on, switched to a new cutoff, turned off and turned on half way,
// at three rates. CI checks the same digest on arm64.
const echoHighpassDigest = "6bcebd00f80965e3a9a99f35a61cbeb312fb94d7093dc131c101ff48c0843870"

func TestEchoHighpassDigest(t *testing.T) {
	d := bitdigest.New()
	settings := []DelayParams{
		{Division: Eighth, Feedback: .7, DampHz: 16_000, Width: 1, Mix: 1, HighpassHz: 300},
		{Division: FreeDelay, TimeMs: 37.5, Feedback: .8, DampHz: 2_500, PingPong: true, Width: .6, Mix: .45, HighpassHz: 1_200},
		{Division: EighthDotted, Feedback: .55, DampHz: 11_000, Width: 1, Mix: .8},
		{Division: Sixteenth, Feedback: .9, DampHz: 6_000, Width: 1, Mix: 1, HighpassHz: 20},
	}
	for i, p := range settings {
		x, err := NewDelay([]int{48_000, 44_100, 96_000, 48_000}[i], 123_456)
		if err != nil {
			t.Fatal(err)
		}
		if err := x.SetParams(p); err != nil {
			t.Fatal(err)
		}
		next := settings[(i+1)%len(settings)]
		runProgram(t, d, x, 40_000, uint32(i)+41, func() error { return x.SetParams(next) }, func() []float64 {
			last := x.buffer[(x.position+len(x.buffer)-1)%len(x.buffer)]
			return []float64{x.feedbackState.left, x.feedbackState.right, x.highpass.left, x.highpass.right, x.highpass.amount, last.left, last.right}
		})
	}
	checkDigest(t, "delay with echo high-pass", d, echoHighpassDigest)
}

// TestEchoHighpassOffNeverRuns: a delay that never sets highpass never runs
// the filter, so its loop is the loop it had before the filter existed (the
// delay digest pins those bits). A filter turned off fades out within the
// smoother and then stops, leaving no state behind.
func TestEchoHighpassOffNeverRuns(t *testing.T) {
	x, err := NewDelay(48_000, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4_800; i++ {
		x.Process(float32(math.Sin(float64(i)*.05)), 0)
	}
	if x.highpass.Active() {
		t.Fatal("the high-pass runs in a delay that never set it")
	}
	p := x.Params()
	p.HighpassHz = 400
	if err := x.SetParams(p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4_800; i++ {
		x.Process(float32(math.Sin(float64(i)*.05)), 0)
	}
	if !x.highpass.Active() || x.highpass.amount < .99 {
		t.Fatalf("the high-pass did not fade in within 100 ms: active %v, amount %g", x.highpass.Active(), x.highpass.amount)
	}
	p.HighpassHz = 0
	if err := x.SetParams(p); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48_000; i++ {
		x.Process(0, 0)
	}
	if x.highpass.Active() || x.highpass.left != 0 || x.highpass.amount != 0 {
		t.Fatalf("the high-pass did not stop within 1 s of off: %+v", x.highpass)
	}
}

// TestEchoHighpassThinsEachRepeat feeds one burst of 80 Hz and one of 2 kHz
// into a delay and a tape-free echo loop and measures every repeat. With the
// high-pass at 300 Hz, the low burst loses about 12 dB more per repeat than
// the loop's own feedback takes, and the high burst keeps nearly all of its
// level: the repeats thin out. Off, both fall only by the feedback.
func TestEchoHighpassThinsEachRepeat(t *testing.T) {
	const rate, delayMs, repeats = 48_000, 250, 4
	for _, c := range []struct {
		highpass           float64
		minLowDrop, maxLow float64 // dB per repeat for the 80 Hz burst
		maxHighDrop        float64 // dB per repeat for the 2 kHz burst
	}{
		{0, 1.5, 2.5, 2.5},
		{300, 12, 16, 2.6},
	} {
		drops := map[float64][]float64{}
		for _, hz := range []float64{80, 2_000} {
			x, err := NewDelay(rate, 120_000)
			if err != nil {
				t.Fatal(err)
			}
			if err := x.SetParams(DelayParams{Division: FreeDelay, TimeMs: delayMs, Feedback: .8, DampHz: 16_000, Width: 1, Mix: 1, HighpassHz: c.highpass}); err != nil {
				t.Fatal(err)
			}
			x.Reset()
			period := rate * delayMs / 1000
			burst := period / 2
			levels := make([]float64, repeats+1)
			for i := 0; i < period*(repeats+1); i++ {
				var in float32
				if i < burst {
					window := .5 - .5*math.Cos(2*math.Pi*float64(i)/float64(burst))
					in = float32(.1 * window * math.Sin(2*math.Pi*hz*float64(i)/rate))
				}
				out, _ := x.Process(in, in)
				levels[i/period] += float64(out) * float64(out)
			}
			for k := 2; k <= repeats; k++ {
				drops[hz] = append(drops[hz], 10*math.Log10(levels[k-1]/levels[k]))
			}
		}
		for _, drop := range drops[80] {
			if drop < c.minLowDrop || drop > c.maxLow {
				t.Errorf("highpass %g Hz: the 80 Hz repeats fall %.2f dB per repeat, want %g to %g dB (all: %.2f)", c.highpass, drop, c.minLowDrop, c.maxLow, drops[80])
			}
		}
		for _, drop := range drops[2_000] {
			if drop > c.maxHighDrop {
				t.Errorf("highpass %g Hz: the 2 kHz repeats fall %.2f dB per repeat, want at most %g dB (all: %.2f)", c.highpass, drop, c.maxHighDrop, drops[2_000])
			}
		}
		t.Logf("highpass %g Hz: dB lost per repeat, 80 Hz %.2f, 2 kHz %.2f", c.highpass, drops[80], drops[2_000])
	}
}
