package modulate

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
)

func q30(v int32) float64 { return float64(v) / One }

// The checked-in table is the rounded sine; this recomputes it.
func TestSineTableMatchesReference(t *testing.T) {
	for i, v := range sineQuarter {
		want := int32(math.Round(math.Sin(float64(i)*math.Pi/2/1024) * One))
		if v != want {
			t.Fatalf("sineQuarter[%d] = %d, want %d", i, v, want)
		}
	}
}

func TestSinAccuracy(t *testing.T) {
	worst := 0.0
	for p := uint64(0); p < 1<<32; p += 7919 {
		got := q30(Sin(uint32(p)))
		want := math.Sin(2 * math.Pi * float64(p) / (1 << 32))
		worst = math.Max(worst, math.Abs(got-want))
	}
	if worst > 3e-7 {
		t.Fatalf("worst sine error %g", worst)
	}
	for _, c := range []struct {
		phase uint32
		want  int32
	}{{0, 0}, {1 << 30, One}, {1 << 31, 0}, {3 << 30, -One}} {
		if got := Sin(c.phase); got != c.want {
			t.Errorf("Sin(%#x) = %d, want %d", c.phase, got, c.want)
		}
	}
}

// clockTick mirrors kernel/seq.Clock.TickAt for a clock anchored at 0: the tick
// and the remainder over 60000*sampleRate.
func clockTick(sample, bpmMilli, rate uint64) (tick, rem, den uint64) {
	den = 60_000 * rate
	n := new(big.Int).Mul(big.NewInt(int64(sample)), big.NewInt(int64(bpmMilli*uint64(seq.PPQ))))
	q, r := new(big.Int).QuoRem(n, big.NewInt(int64(den)), new(big.Int))
	return q.Uint64(), r.Uint64(), den
}

// Spec 24, lane 1 tests: rate = 1/4 at 120 BPM and 48 kHz gives 0, 1, 0, -1 at
// samples 0, 6000, 12000, 18000, and the grid point 47 (sample 6016) agrees
// with the function there.
func TestLFOSpecVector(t *testing.T) {
	for _, c := range []struct {
		sample uint64
		want   float64
	}{{0, 0}, {6000, 1}, {12000, 0}, {18000, -1}, {24000, 0}} {
		tick, rem, den := clockTick(c.sample, 120_000, 48000)
		got := q30(LFO(Sine, 4242, 0, SyncedPosition(tick, rem, den, uint64(seq.PPQ))))
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("sample %d: %g, want %g", c.sample, got, c.want)
		}
	}
	tick, rem, den := clockTick(47*GridFrames, 120_000, 48000)
	got := q30(LFO(Sine, 4242, 0, SyncedPosition(tick, rem, den, uint64(seq.PPQ))))
	want := math.Sin(2 * math.Pi * 6016 / 24000)
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("grid point 47: %g, want %g", got, want)
	}
}

func TestOtherWaves(t *testing.T) {
	for _, c := range []struct {
		wave  Wave
		phase float64
		want  float64
	}{
		{Triangle, 0, 0}, {Triangle, .125, .5}, {Triangle, .25, 1}, {Triangle, .5, 0}, {Triangle, .75, -1}, {Triangle, .875, -.5},
		{Square, 0, 1}, {Square, .49, 1}, {Square, .5, -1}, {Square, .99, -1},
	} {
		got := q30(LFO(c.wave, 1, 0, Position{Phase: uint32(c.phase * (1 << 32))}))
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("wave %d at %g: %g, want %g", c.wave, c.phase, got, c.want)
		}
	}
}

func bigPosition(n *big.Int, d *big.Int) Position {
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	f := new(big.Int).Lsh(r, 32)
	f.Quo(f, d)
	return Position{Cycle: q.Uint64(), Phase: uint32(f.Uint64())}
}

func TestPositionsMatchBigArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20000; i++ {
		rate := []uint64{44100, 48000, 96000, 192000}[rng.Intn(4)]
		sample := rng.Uint64() >> uint(20+rng.Intn(20))
		micro := 10_000 + rng.Uint64()%19_990_000
		got := FreePosition(sample, uint32(rate), micro)
		want := bigPosition(new(big.Int).Mul(new(big.Int).SetUint64(sample), new(big.Int).SetUint64(micro)), new(big.Int).SetUint64(rate*1_000_000))
		if got != want {
			t.Fatalf("FreePosition(%d,%d,%d) = %+v, want %+v", sample, rate, micro, got, want)
		}
		// Synced: the Q32 truncation of the remainder is part of the definition.
		bpm := 20_000 + rng.Uint64()%280_000
		period := 1 + rng.Uint64()%(1<<28)
		tick, rem, den := clockTick(sample, bpm, rate)
		g := SyncedPosition(tick, rem, den, period)
		frac := new(big.Int).Lsh(new(big.Int).SetUint64(rem), 32)
		frac.Quo(frac, new(big.Int).SetUint64(den))
		exact := new(big.Int).Lsh(new(big.Int).SetUint64(tick), 32)
		exact.Add(exact, frac)
		w := bigPosition(exact, new(big.Int).Lsh(new(big.Int).SetUint64(period), 32))
		if g != w {
			t.Fatalf("SyncedPosition(%d,%d,%d,%d) = %+v, want %+v", tick, rem, den, period, g, w)
		}
	}
}

func TestFitPositionMatchesBigArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 20000; i++ {
		loop := 1 + rng.Uint64()%(1<<36)
		cycles := 1 + rng.Uint64()%4096
		at := rng.Uint64() % loop
		den := 1 + rng.Uint64()%(1<<34)
		num := rng.Uint64() % den
		got := FitPosition(at, num, den, loop, cycles)
		frac := new(big.Int).Lsh(new(big.Int).SetUint64(num), 32)
		frac.Quo(frac, new(big.Int).SetUint64(den))
		exact := new(big.Int).Lsh(new(big.Int).SetUint64(at), 32)
		exact.Add(exact, frac)
		exact.Mul(exact, new(big.Int).SetUint64(cycles))
		want := bigPosition(exact, new(big.Int).Lsh(new(big.Int).SetUint64(loop), 32))
		if got != want {
			t.Fatalf("FitPosition(%d,%d,%d,%d,%d) = %+v, want %+v", at, num, den, loop, cycles, got, want)
		}
	}
	// No jump at the wrap: the position one sample before the end is just
	// below a whole cycle count, and the start is zero.
	last := FitPosition(7999, 0, 1, 8000, 12)
	if last.Cycle != 11 || last.Phase < 4280000000 {
		t.Fatalf("end of loop = %+v", last)
	}
	if first := FitPosition(0, 0, 1, 8000, 12); first != (Position{}) {
		t.Fatalf("start of loop = %+v", first)
	}
}

func TestFittedRandomSourcesWrapAtLoopBoundary(t *testing.T) {
	for _, cycles := range []uint64{1, 3, 8} {
		for octaves := uint(1); octaves <= 4; octaves++ {
			first := SwayFitted(4242, 1, octaves, Position{}, cycles)
			end := SwayFitted(4242, 1, octaves, Position{Cycle: cycles}, cycles)
			near := SwayFitted(4242, 1, octaves, Position{Cycle: cycles - 1, Phase: ^uint32(0)}, cycles)
			if end != first || abs64(int64(near)-int64(first)) > 16 {
				t.Fatalf("cycles=%d octaves=%d: start=%d near=%d end=%d", cycles, octaves, first, near, end)
			}
		}
		first := LFOFitted(SampleHold, 4242, 1, Position{}, cycles)
		if end := LFOFitted(SampleHold, 4242, 1, Position{Cycle: cycles}, cycles); end != first {
			t.Fatalf("sample-hold cycles=%d: start=%d end=%d", cycles, first, end)
		}
	}
	p := Position{Cycle: 23, Phase: 1 << 30}
	if got, want := SwayFitted(4242, 1, 3, p, 0), Sway(4242, 1, 3, p); got != want {
		t.Fatalf("zero cycles changed ordinary sway: got %d want %d", got, want)
	}
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// The draws for seed 4242 are pinned: a change to the hash, the domain layout
// or the mapping changes every score that uses a sample-hold or a sway.
func TestReferenceVectors(t *testing.T) {
	var sh, sw []string
	for k := uint64(0); k < 16; k++ {
		sh = append(sh, fmt.Sprint(LFO(SampleHold, 4242, 0, Position{Cycle: k})))
	}
	for k := uint64(0); k < 16; k++ {
		sw = append(sw, fmt.Sprint(Sway(4242, 1, 2, Position{Cycle: k / 4, Phase: uint32(k%4) << 30})))
	}
	check := func(name, got, want string) {
		if got != want {
			t.Errorf("%s vector changed:\n got %s\nwant %s", name, got, want)
		}
	}
	check("sample-hold", strings.Join(sh, " "), referenceSampleHold)
	check("sway", strings.Join(sw, " "), referenceSway)
	// Two sway declarations differ.
	if Sway(4242, 0, 2, Position{Cycle: 3, Phase: 1 << 30}) == Sway(4242, 1, 2, Position{Cycle: 3, Phase: 1 << 30}) {
		t.Error("two sway sources with different indexes draw the same value")
	}
}

func TestDomainZeroMatchesChanceHash(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 5000; i++ {
		seed := rng.Uint32()
		track, slot, step := uint8(rng.Intn(64)), uint8(rng.Intn(8)), uint8(rng.Intn(64))
		iter := int64(rng.Intn(100000))
		u := Hash(seed, 0, track, slot, step, uint64(iter)) >> 40
		for _, p := range []uint8{1, 25, 50, 70, 99} {
			want := seq.ProbabilityHit(p, seed, track, slot, iter, step)
			if got := u*100 < uint64(p)<<24; got != want {
				t.Fatalf("chance %d differs for seed %d track %d slot %d step %d iteration %d", p, seed, track, slot, step, iter)
			}
		}
	}
}

func TestSwayIsSmoothAndBounded(t *testing.T) {
	for _, octaves := range []uint{1, 2, 3, 4} {
		prev := Sway(9, 0, octaves, Position{})
		var biggest int64
		for k := uint64(0); k < 64*1024; k++ {
			p := Position{Cycle: k >> 10, Phase: uint32(k&1023) << 22}
			v := Sway(9, 0, octaves, p)
			if v > One || v < -One {
				t.Fatalf("octaves %d: value %d out of range", octaves, v)
			}
			if d := int64(v) - int64(prev); d > biggest {
				biggest = d
			} else if -d > biggest {
				biggest = -d
			}
			prev = v
		}
		// One step is 1/1024 of a base period; the fastest layer moves at most
		// 2^(octaves-1)*1.5 of full swing per period.
		if limit := int64(One) * 3 << octaves / 1024; biggest > limit {
			t.Errorf("octaves %d: step %d exceeds %d", octaves, biggest, limit)
		}
	}
}

func TestContourShapes(t *testing.T) {
	half := uint64(1 << 31)
	cases := []struct {
		shape Shape
		at    float64 // fraction of the segment
		want  float64
	}{
		{Linear, .5, 0}, {Smooth, .25, -1 + 2*0.15625}, {Exponential, .5, 0}, {Step, .5, -1}, {Step, .9999, -1},
	}
	for _, c := range cases {
		points := []Point{{0, -One, Linear}, {half, One, c.shape}, {1 << 32, -One, Linear}}
		got := q30(Contour(points, Position{Phase: uint32(c.at * float64(half))}))
		if math.Abs(got-c.want) > 1e-6 {
			t.Errorf("shape %d at %g: %g, want %g", c.shape, c.at, got, c.want)
		}
	}
	points := []Point{{0, -One, Linear}, {1 << 30, One, Smooth}, {1 << 32, -One, Linear}}
	if got := Contour(points, Position{Phase: 1 << 30}); got != One {
		t.Errorf("at a point: %d", got)
	}
	if got := Contour(points, Position{Phase: 0}); got != -One {
		t.Errorf("at start: %d", got)
	}
}

func TestFollower(t *testing.T) {
	var f Follower
	attack, release := uint32(1<<31), uint32(1<<28) // half and a sixteenth per grid point
	if got := f.Step(One, attack, release); got != One/2 {
		t.Fatalf("first attack step = %d", got)
	}
	if got := f.Step(One, attack, release); got != One*3/4 {
		t.Fatalf("second attack step = %d", got)
	}
	before := f.Level
	if got := f.Step(0, attack, release); got >= before || got <= 0 {
		t.Fatalf("release step = %d from %d", got, before)
	}
	for i := 0; i < 400; i++ {
		f.Step(0, attack, release)
	}
	if f.Level != 0 {
		t.Fatalf("follower never reached zero: %d", f.Level)
	}
	f.Step(One, attack, release)
	f.Reset()
	if f.Level != 0 {
		t.Fatalf("Reset left %d", f.Level)
	}
}

func TestEvaluatorsDoNotAllocate(t *testing.T) {
	points := []Point{{0, -One, Linear}, {1 << 31, One, Smooth}, {1 << 32, -One, Linear}}
	var sink int32
	var f Follower
	n := testing.AllocsPerRun(200, func() {
		p := SyncedPosition(123456, 77, 2_880_000, 3840)
		q := FreePosition(987654321, 48000, 70_000)
		r := FitPosition(1000, 3, 7, 80000, 12)
		sink += LFO(Sine, 1, 0, p) + LFO(SampleHold, 1, 1, q) + Sway(1, 2, 4, r) + Contour(points, p) + f.Step(One, 1<<30, 1<<20)
	})
	if n != 0 {
		t.Fatalf("%v allocations", n)
	}
	_ = sink
}
