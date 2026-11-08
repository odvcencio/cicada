package tape

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
)

var rates = []int{44_100, 48_000, 96_000}

func newTape(t testing.TB, rate int, p Params) *Tape {
	t.Helper()
	tp, err := New(rate, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := tp.SetParams(p); err != nil {
		t.Fatal(err)
	}
	tp.Reset()
	return tp
}

func free(ms float64) Params {
	return Params{Division: fx.FreeDelay, TimeMs: ms, DampHz: 16_000}
}

type noise uint32

func (n *noise) next() float32 {
	*n = *n*1664525 + 1013904223
	return float32(int32(*n)>>8) / (1 << 23)
}

func TestImpulseRepeatsAtTime(t *testing.T) {
	for _, rate := range rates {
		p := free(100)
		p.Feedback = .5
		tp := newTape(t, rate, p)
		want := rate / 10
		var peaks []int
		for i := 0; i < 4*want+10; i++ {
			in := float32(0)
			if i == 0 {
				in = 1
			}
			l, _ := tp.Process(in, in)
			if l > .01 {
				peaks = append(peaks, i)
			}
		}
		// Damping at 16 kHz spreads each repeat over a few frames; the first
		// frame above the threshold must sit on a multiple of the time.
		if len(peaks) < 4 || peaks[0] != want {
			t.Fatalf("rate %d: first repeat at %v, want %d", rate, peaks, want)
		}
	}
}

func TestHeadStaysWithinModulationDepth(t *testing.T) {
	for _, rate := range rates {
		p := free(300)
		p.Wow, p.Flutter = 1, 1
		tp := newTape(t, rate, p)
		centre := 300 * float64(rate) / 1000
		bound := (WowDepthMs + FlutterDepthMs) * float64(rate) / 1000
		var lo, hi float64 = math.MaxFloat64, -math.MaxFloat64
		for i := 0; i < 10*rate; i++ {
			d := tp.advance()
			lo, hi = math.Min(lo, d), math.Max(hi, d)
		}
		if lo < centre-bound-1e-6 || hi > centre+bound+1e-6 {
			t.Fatalf("rate %d: head range [%f %f] outside %f +- %f", rate, lo, hi, centre, bound)
		}
		if hi-lo < bound { // the modulation must actually move
			t.Fatalf("rate %d: head moved only %f frames", rate, hi-lo)
		}
	}
}

func TestStaticHeadWithoutModulation(t *testing.T) {
	p := free(250)
	tp := newTape(t, 48_000, p)
	for i := 0; i < 48_000; i++ {
		if d := tp.advance(); d != 250*48 {
			t.Fatalf("frame %d: delay %f, want 12000", i, d)
		}
	}
}

// countHz estimates frequency from rising zero crossings.
func countHz(x []float32, rate int) float64 {
	n := 0
	first, last := -1, -1
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			if first < 0 {
				first = i
			}
			last = i
			n++
		}
	}
	if n < 3 {
		return 0
	}
	return float64(n-1) * float64(rate) / float64(last-first)
}

func TestTimeChangeGlidesPitch(t *testing.T) {
	const rate, hz = 48_000, 1000.0
	p := free(400)
	tp := newTape(t, rate, p)
	phase := 0.0
	sine := func() float32 {
		phase += 2 * math.Pi * hz / rate
		return float32(.5 * math.Sin(phase))
	}
	for i := 0; i < rate; i++ { // settle
		tp.Process(sine(), 0)
	}
	if got := tp.Head(); got != 400*48 {
		t.Fatalf("head %f", got)
	}
	p.TimeMs = 200
	if err := tp.SetParams(p); err != nil {
		t.Fatal(err)
	}
	// 200 ms = 9600 frames at 0.5 frames/frame is 19200 frames of glide.
	out := make([]float32, 19_200)
	for i := range out {
		out[i], _ = tp.Process(sine(), 0)
	}
	mid := countHz(out[4000:15000], rate)
	if mid < 1.45*hz || mid > 1.55*hz {
		t.Fatalf("repeat pitch during shortening glide %.0f Hz, want about %.0f", mid, 1.5*hz)
	}
	if tp.Head() != 200*48 {
		t.Fatalf("head ended at %f", tp.Head())
	}
	after := make([]float32, 8000)
	for i := range after {
		after[i], _ = tp.Process(sine(), 0)
	}
	if got := countHz(after[2000:], rate); math.Abs(got-hz) > 20 {
		t.Fatalf("pitch after glide %.0f Hz, want %.0f", got, hz)
	}
	// Lengthening plays slower.
	p.TimeMs = 400
	_ = tp.SetParams(p)
	for i := range out {
		out[i], _ = tp.Process(sine(), 0)
	}
	if low := countHz(out[4000:15000], rate); low < .45*hz*1.0 || low > .55*hz*1.0+60 {
		t.Fatalf("repeat pitch during lengthening glide %.0f Hz, want about %.0f", low, .5*hz)
	}
}

func TestTempoChangeGlides(t *testing.T) {
	p := Params{Division: fx.Eighth, DampHz: 16_000}
	tp := newTape(t, 48_000, p)
	before := tp.Head()
	if err := tp.SetTempo(240_000); err != nil {
		t.Fatal(err)
	}
	tp.advance()
	if got := tp.Head(); got >= before || before-got > MaxSlew+1e-9 {
		t.Fatalf("head jumped from %f to %f", before, got)
	}
}

func TestSaturationBoundsLoop(t *testing.T) {
	for _, sat := range []float64{0, 1} {
		p := free(50)
		p.Feedback, p.Saturation = .95, sat
		tp := newTape(t, 48_000, p)
		var n noise = 7
		peak := 0.0
		for i := 0; i < 48_000; i++ {
			in := n.next()
			l, r := tp.Process(in, in)
			peak = math.Max(peak, math.Max(math.Abs(float64(l)), math.Abs(float64(r))))
		}
		bound := 1 + 1/(1+3*sat) // input peak plus the saturated loop contribution
		if peak > bound+1e-6 {
			t.Fatalf("saturation %v: peak %f exceeds %f", sat, peak, bound)
		}
		rms := func(frames int) float64 {
			var sum float64
			for i := 0; i < frames; i++ {
				l, _ := tp.Process(0, 0)
				sum += float64(l) * float64(l)
			}
			return math.Sqrt(sum / float64(frames))
		}
		early := rms(4800)
		for i := 0; i < 20*48_000; i++ {
			tp.Process(0, 0)
		}
		late := rms(4800)
		if !(late < early) || math.IsNaN(late) {
			t.Fatalf("saturation %v: tail did not decay (%g then %g)", sat, early, late)
		}
	}
}

func TestSilenceInSilenceOut(t *testing.T) {
	p := DefaultParams()
	p.Division, p.TimeMs = fx.FreeDelay, 120
	tp := newTape(t, 44_100, p)
	for i := 0; i < 100_000; i++ {
		if l, r := tp.Process(0, 0); l != 0 || r != 0 {
			t.Fatalf("frame %d: %g %g", i, l, r)
		}
	}
	if !tp.Silent() {
		t.Fatal("tape reports excited after silence")
	}
}

func TestResetClears(t *testing.T) {
	p := free(20)
	p.Feedback = .9
	tp := newTape(t, 48_000, p)
	var n noise = 3
	for i := 0; i < 5000; i++ {
		tp.Process(n.next(), n.next())
	}
	if tp.Silent() {
		t.Fatal("should be excited")
	}
	tp.Reset()
	if !tp.Silent() {
		t.Fatal("reset left tape excited")
	}
	for i := 0; i < 20_000; i++ {
		if l, r := tp.Process(0, 0); l != 0 || r != 0 {
			t.Fatalf("frame %d after reset: %g %g", i, l, r)
		}
	}
}

func TestProcessAllocatesNothing(t *testing.T) {
	p := DefaultParams()
	p.Division, p.TimeMs = fx.FreeDelay, 300
	tp := newTape(t, 48_000, p)
	var n noise = 1
	q := p
	allocs := testing.AllocsPerRun(200, func() {
		for i := 0; i < 256; i++ {
			tp.Process(n.next(), n.next())
		}
		q.TimeMs = 280 + float64(n.next())*10
		if tp.SetParams(q) != nil {
			t.Fatal("set")
		}
	})
	_ = allocs
	proc := testing.AllocsPerRun(200, func() {
		for i := 0; i < 256; i++ {
			tp.Process(.25, -.25)
		}
	})
	if proc != 0 {
		t.Fatalf("Process allocated %v times", proc)
	}
}

func TestFiniteAtAllRates(t *testing.T) {
	for _, rate := range rates {
		p := DefaultParams()
		p.Feedback, p.Wow, p.Flutter, p.Saturation = .95, 1, 1, 1
		p.Division, p.TimeMs = fx.FreeDelay, 1
		tp := newTape(t, rate, p)
		var n noise = 11
		for i := 0; i < 3*rate; i++ {
			if i%rate == rate/2 {
				p.TimeMs = 2000 - p.TimeMs/2
				if err := tp.SetParams(p); err != nil {
					t.Fatal(err)
				}
			}
			l, r := tp.Process(n.next(), n.next())
			if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
				t.Fatalf("rate %d frame %d: %g %g", rate, i, l, r)
			}
		}
		if tp.Fault() {
			t.Fatalf("rate %d faulted", rate)
		}
	}
}

func TestRejectsBadParams(t *testing.T) {
	bad := []Params{
		{Division: fx.Eighth, Feedback: .96, DampHz: 5000},
		{Division: fx.Eighth, Wow: 1.1, DampHz: 5000},
		{Division: fx.Eighth, Saturation: -.1, DampHz: 5000},
		{Division: fx.Eighth, DampHz: 500},
		{Division: fx.FreeDelay, TimeMs: 2001, DampHz: 5000},
		{Division: fx.Eighth, TimeMs: 100, DampHz: 5000},
		{Division: fx.Eighth, Wow: math.NaN(), DampHz: 5000},
	}
	for i, p := range bad {
		if p.Validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if (Params{Division: fx.Half, DampHz: 5000}).ValidateTempo(20_000) == nil {
		t.Error("a half note at 20 BPM is 6 s and must be refused")
	}
}

// With wow, flutter and saturation at zero, and the delay's damping, no
// ping-pong, width 1 and mix 1, tape output matches the delay while the time
// holds still. When the time is a whole number of frames the match is byte for
// byte. When it is fractional the two differ by float64 rounding in the read
// position, because the delay's ring is 4 s long and the tape's is 2 s, so
// the wrapped position rounds differently. Float32 output then differs by at
// most a few ulps; the test bounds it at 1e-6, which is -120 dB.
func TestMatchesDelayByteForByte(t *testing.T) {
	cases := []struct {
		division fx.DelayDivision
		ms       float64
		feedback float64
		damp     float64
		bpm      int64
		exact    bool
	}{
		{fx.FreeDelay, 250, .6, 3200, 74_000, true},
		{fx.FreeDelay, 500, .95, 1000, 120_000, true},
		{fx.ThreeSixteenths, 0, .6, 3200, 74_000, false},
		{fx.FreeDelay, 333.3, .9, 5000, 120_000, false},
		{fx.EighthDotted, 0, .95, 1000, 150_000, false},
	}
	for _, rate := range rates {
		for _, c := range cases {
			d, err := fx.NewDelay(rate, c.bpm)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetParams(fx.DelayParams{Division: c.division, TimeMs: c.ms, Feedback: c.feedback, DampHz: c.damp, Width: 1, Mix: 1}); err != nil {
				t.Fatal(err)
			}
			d.Reset()
			tp, _ := New(rate, c.bpm)
			if err := tp.SetParams(Params{Division: c.division, TimeMs: c.ms, Feedback: c.feedback, DampHz: c.damp}); err != nil {
				t.Fatal(err)
			}
			tp.Reset()
			var n noise = 5
			worst, differing := 0.0, 0
			for i := 0; i < 4*rate; i++ {
				var l, r float32
				if i < rate/4 { // a burst, then the tail
					l, r = n.next(), n.next()
				}
				dl, dr := d.Process(l, r)
				tl, tr := tp.Process(l, r)
				if dl != tl || dr != tr {
					differing++
					if c.exact {
						t.Fatalf("rate %d case %+v frame %d: delay %g %g, tape %g %g", rate, c, i, dl, dr, tl, tr)
					}
					worst = math.Max(worst, math.Max(math.Abs(float64(dl-tl)), math.Abs(float64(dr-tr))))
				}
			}
			if worst > 1e-6 {
				t.Fatalf("rate %d case %+v: worst difference %g", rate, c, worst)
			}
			t.Logf("rate %d case %v/%vms: %d differing frames, worst %.2g", rate, c.division, c.ms, differing, worst)
		}
	}
}

func BenchmarkProcess(b *testing.B) {
	p := DefaultParams()
	p.Division, p.TimeMs = fx.FreeDelay, 300
	tp := newTape(b, 48_000, p)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tp.Process(.1, -.1)
	}
}
