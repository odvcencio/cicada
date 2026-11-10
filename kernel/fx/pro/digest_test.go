package pro

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// The digests below pin each master-chain processor's output bits for an
// exact test programme. amd64 never fuses a multiply and an add; arm64 does
// unless an explicit conversion rounds the product first. CI checks the same
// digests on arm64 in the PCM24 parity job.

// feedProgram feeds frames of the test programme, scaled by gain (a power of
// two, so the input stays exact), into process and adds every output sample,
// and the values state returns if it is set, to d. The float32 output rounds
// away most last-bit differences; float64 state does not.
func feedProgram(d *bitdigest.Digest, process func(float32, float32) (float32, float32), frames int, seed uint32, gain float32, state func() []float64) {
	left, right := bitdigest.NewNoise(seed), bitdigest.NewNoise(seed^0x5bd1e995)
	for i := 0; i < frames; i++ {
		l, r := bitdigest.Program(left, right, i)
		d.Float32(process(l*gain, r*gain))
		if state != nil {
			d.Float64(state()...)
		}
	}
}

func checkProDigest(t *testing.T, name string, d *bitdigest.Digest, want string) {
	t.Helper()
	if got := d.Hex(); got != want {
		t.Fatalf("%s digest = %s, want %s", name, got, want)
	}
}

const limiterDigest = "3116391bcb15f5c56eabf6315cdf4781420b804d930b7aa34e3d34e63de51842"

func TestLimiterDigest(t *testing.T) {
	d := bitdigest.New()
	for i, p := range []LimiterParams{
		DefaultLimiterParams(),
		{CeilingDBTP: -6, LookaheadMs: 1, ReleaseMs: 20},
		{CeilingDBTP: 0, LookaheadMs: 10, ReleaseMs: 1000},
	} {
		l, err := NewLimiter([]int{48_000, 44_100, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		feedProgram(d, l.Process, 40_000, uint32(i)+51, 4, func() []float64 { return []float64{l.gain} })
	}
	checkProDigest(t, "limiter", d, limiterDigest)
}

const transientDigest = "92ff1e8fbe44e1483e81ebdc71e2834617660f47e7c2227f14264fbc48dcced4"

func TestTransientDigest(t *testing.T) {
	d := bitdigest.New()
	for i, p := range []TransientParams{
		{Enabled: true, AttackDB: 9, SustainDB: -6, FastMs: .5, SlowMs: 40, ReleaseMs: 200},
		{Enabled: true, AttackDB: -12, SustainDB: 6, FastMs: 5, SlowMs: 90, ReleaseMs: 50},
	} {
		x, err := NewTransient([]int{48_000, 44_100}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		feedProgram(d, x.Process, 40_000, uint32(i)+61, 1, func() []float64 { return []float64{x.fast, x.slow, x.gain} })
	}
	checkProDigest(t, "transient", d, transientDigest)
}

const widthDigest = "006a67d54183d3985f1450c097779233b252c1cb2a2038bee81da0e472929a3d"

func TestWidthDigest(t *testing.T) {
	d := bitdigest.New()
	for i, p := range []WidthParams{
		{Enabled: true, Amount: 1.6},
		{Enabled: true, Amount: .3, BassMonoHz: 150},
		{Enabled: true, Amount: 2, BassMonoHz: 480},
	} {
		x, err := NewWidth([]int{48_000, 44_100, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		feedProgram(d, x.Process, 30_000, uint32(i)+71, 1, nil)
	}
	// Width keeps no state, and its float32 output hides a fused product
	// except where mid ± side*1.6 lands next to a float32 rounding boundary.
	// These inputs do: rounding the product once or twice gives a different
	// output (found by searching against math.FMA), so a fused product
	// changes the digest.
	x, err := NewWidth(48_000, WidthParams{Enabled: true, Amount: 1.6})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]uint32{
		{0xbed0f886, 0xbdc0e568}, {0xbc39d480, 0xbd4950e0}, {0x3ce78300, 0x3bd5b400},
		{0x3ea24a60, 0x3d95ce80}, {0xbeab8f7c, 0xbd9e5d10}, {0x3d2420b0, 0x3e31ce14},
	} {
		d.Float32(x.Process(math.Float32frombits(pair[0]), math.Float32frombits(pair[1])))
	}
	checkProDigest(t, "width", d, widthDigest)
}

const roomDigest = "62dea67f50a1635bc805838c7a60828b40db00fdf8ecdd469ff6c57229b8e37d"

func TestRoomDigest(t *testing.T) {
	d := bitdigest.New()
	for i, p := range []ReverbParams{
		{Enabled: true, Tail: fx.DefaultReverbParams(), EarlyMix: .5, Width: 1.5, Mix: .4},
		{Enabled: true, Tail: fx.ReverbParams{Size: .7, DecaySec: 1.2, DampHz: 5_000, HighpassHz: 200, PredelayMs: 15, Mix: 1}, EarlyMix: .1, Width: .5, Mix: .8},
	} {
		x, err := NewReverb([]int{48_000, 96_000}[i], p)
		if err != nil {
			t.Fatal(err)
		}
		feedProgram(d, x.Process, 40_000, uint32(i)+81, 1, nil)
	}
	checkProDigest(t, "room", d, roomDigest)
}

const chainDigest = "fe131f43079aadc0afd85f1c09c876844cd4b4629fabf16aabf49d3e2b67d8d7"

// The whole chain, from each preset and from one setting with every stage on.
func TestChainDigest(t *testing.T) {
	d := bitdigest.New()
	var settings []Params
	for _, name := range PresetNames {
		p, ok := Preset(name)
		if !ok {
			t.Fatalf("preset %q", name)
		}
		settings = append(settings, p)
	}
	all := DefaultParams()
	all.EQ = [4]Band{
		{Enabled: true, Type: Highpass, FrequencyHz: 30, Q: .707},
		{Enabled: true, Type: LowShelf, FrequencyHz: 120, Q: 1, GainDB: 2.5},
		{Enabled: true, Type: Peak, FrequencyHz: 2_700, Q: 1.8, GainDB: -3.25},
		{Enabled: true, Type: HighShelf, FrequencyHz: 9_000, Q: 1, GainDB: 4},
	}
	all.EnableCompressor = true
	all.Compressor = fx.CompParams{Detect: fx.RMSDetector, Threshold: -24, Ratio: 3, Knee: 8, AttackMs: 5, ReleaseMs: 150, MakeupAuto: true, Mix: .9}
	all.Transient = TransientParams{Enabled: true, AttackDB: 4, SustainDB: -2, FastMs: 1, SlowMs: 30, ReleaseMs: 120}
	all.Width = WidthParams{Enabled: true, Amount: 1.3, BassMonoHz: 120}
	all.Reverb = DefaultReverbParams()
	all.Reverb.Enabled = true
	all.EnableLimiter = true
	all.Limiter = LimiterParams{CeilingDBTP: -1, LookaheadMs: 2, ReleaseMs: 80}
	settings = append(settings, all)
	for i, p := range settings {
		c, err := New([]int{48_000, 44_100, 96_000}[i%3], p)
		if err != nil {
			t.Fatalf("setting %d: %v", i, err)
		}
		feedProgram(d, c.Process, 24_000, uint32(i)+91, 2, func() []float64 { return []float64{c.LimiterGainReductionDB()} })
	}
	checkProDigest(t, "chain", d, chainDigest)
}

const normalizeDigest = "656a5d2fc190ef3df371afefebeaf8c5061e004bf69c2ea887003210b261617a"

// NormalizeStereo measures, chooses a gain and applies it; the digest covers
// the gain, the readings and the output.
func TestNormalizeDigest(t *testing.T) {
	d := bitdigest.New()
	for i, target := range [][2]float64{{-14, -1}, {-9, -1}, {-23, -2}} {
		rate := []int{48_000, 44_100, 96_000}[i]
		left, right := make([]float32, 3*rate), make([]float32, 3*rate)
		ln, rn := bitdigest.NewNoise(uint32(i)+101), bitdigest.NewNoise(uint32(i)+102)
		for j := range left {
			left[j], right[j] = bitdigest.Program(ln, rn, j)
		}
		n, err := NormalizeStereo(rate, left, right, target[0], target[1])
		if err != nil {
			t.Fatal(err)
		}
		d.Float64(n.AppliedGainDB, n.Before.IntegratedLUFS, n.Before.TruePeak, n.After.IntegratedLUFS, n.After.TruePeak, n.After.TruePeakDBTP)
		d.Int(boolInt(n.PeakLimited), boolInt(n.Reachable))
		d.Float32(left...)
		d.Float32(right...)
	}
	checkProDigest(t, "normalize", d, normalizeDigest)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
