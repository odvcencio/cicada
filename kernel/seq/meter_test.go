package seq

import (
	"math/rand"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

// The zero map must be the fixed 3,840-tick bar it replaces, bit for bit,
// including negative ticks and the arithmetic of every removed expression.
func TestZeroMeterMapIsTheFixedBar(t *testing.T) {
	const bar = int64(3840)
	var m MeterMap
	rng := rand.New(rand.NewSource(7))
	ticks := []int64{0, 1, 239, 240, 959, 960, 3839, 3840, 3841, 7679, 7680, -1, -3839, -3840, -3841, 1 << 40, 1<<53 - 1}
	for i := 0; i < 20000; i++ {
		ticks = append(ticks, rng.Int63n(1<<45)-1<<20)
	}
	for _, tick := range ticks {
		if b, o := m.BarAt(tick); b != tick/bar || o != tick%bar {
			t.Fatalf("BarAt(%d) = %d, %d", tick, b, o)
		}
		if got := m.NextBarLine(tick); got != (tick/bar+1)*bar {
			t.Fatalf("NextBarLine(%d) = %d", tick, got)
		}
		if got := m.BarsUntil(tick); got != (tick+bar-1)/bar {
			t.Fatalf("BarsUntil(%d) = %d", tick, got)
		}
		if got := m.IsBarLine(tick); got != (tick%bar == 0) {
			t.Fatalf("IsBarLine(%d) = %t", tick, got)
		}
		if got := m.BarsTicks(tick, 3); got != 3*bar {
			t.Fatalf("BarsTicks(%d, 3) = %d", tick, got)
		}
		if n, whole := m.WholeBars(tick, tick); n != tick/bar || whole != (tick%bar == 0) {
			t.Fatalf("WholeBars(%d) = %d, %t", tick, n, whole)
		}
		if tick >= 0 {
			for _, every := range []int64{1, 2, 4, 16} {
				if got := m.QuantizeBars(tick, every); got != nextMultiple(tick, every*bar) {
					t.Fatalf("QuantizeBars(%d, %d) = %d", tick, every, got)
				}
			}
			if got := m.QuantizeBeat(tick); got != nextMultiple(tick, PPQ) {
				t.Fatalf("QuantizeBeat(%d) = %d", tick, got)
			}
			b, beat, step, rest := m.Position(tick)
			if b != tick/bar+1 || beat != tick%bar/PPQ+1 || step != tick%PPQ/TicksPerStep+1 || rest != tick%TicksPerStep {
				t.Fatalf("Position(%d) = %d.%d.%d+%d", tick, b, beat, step, rest)
			}
		}
	}
	for b := int64(-3); b < 100; b++ {
		if m.BarStart(b) != b*bar || m.BarTicks(b) != bar || m.BeatTicks(b) != PPQ || m.BeatsInBar(b) != 4 {
			t.Fatalf("bar %d: start %d, %d ticks, beat %d, %d beats", b, m.BarStart(b), m.BarTicks(b), m.BeatTicks(b), m.BeatsInBar(b))
		}
	}
	for _, p := range [][3]int64{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {1, 5, 1}, {1, 1, 5}, {-3, 1, 1}} {
		if _, ok := m.PositionTick(p[0], p[1], p[2]); ok {
			t.Fatalf("the zero map accepted @%d.%d.%d", p[0], p[1], p[2])
		}
	}
	explicit, err := NewMeterMap([]MeterChange{{Bar: 0, Meter: Meter{Beats: 4, Unit: 4}}})
	if err != nil || explicit.segments != nil {
		t.Fatalf("a single 4/4 change is not the zero map: %+v, %v", explicit, err)
	}
}

func TestNewMeterMapRejectsInvalidChanges(t *testing.T) {
	for _, changes := range [][]MeterChange{
		nil,
		{{Bar: 1, Meter: Meter{4, 4}}},
		{{Bar: 0, Meter: Meter{0, 4}}},
		{{Bar: 0, Meter: Meter{33, 4}}},
		{{Bar: 0, Meter: Meter{4, 3}}},
		{{Bar: 0, Meter: Meter{4, 1}}},
		{{Bar: 0, Meter: Meter{4, 32}}},
		{{Bar: 0, Meter: Meter{32, 2}}},
		{{Bar: 0, Meter: Meter{4, 4}}, {Bar: 0, Meter: Meter{3, 4}}},
		{{Bar: 0, Meter: Meter{4, 4}}, {Bar: 4, Meter: Meter{3, 4}}, {Bar: 3, Meter: Meter{4, 4}}},
		{{Bar: 0, Meter: Meter{4, 4}}, {Bar: maxMeterBar + 1, Meter: Meter{3, 4}}},
	} {
		if _, err := NewMeterMap(changes); err == nil {
			t.Errorf("accepted %+v", changes)
		}
	}
	if m, err := NewMeterMap([]MeterChange{{Bar: 0, Meter: Meter{32, 4}}}); err != nil || m.BarTicks(0) != MaxBarTicks {
		t.Fatalf("32/4 is the longest bar: %d, %v", m.BarTicks(0), err)
	}
}

// The spec's 4/4 to 7/8 and back example: bars 5 to 8 (zero-based 4 to 7) are
// 3,360 ticks, so bar 9 starts at 28,800.
func TestMeterMapWorkedExample(t *testing.T) {
	m, err := NewMeterMap([]MeterChange{{0, Meter{4, 4}}, {4, Meter{7, 8}}, {8, Meter{4, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.BarStart(8); got != 28_800 {
		t.Fatalf("bar 9 starts at %d", got)
	}
	if got := m.BarTicks(5); got != 3360 || m.BeatTicks(5) != 480 || m.BeatsInBar(5) != 7 {
		t.Fatalf("7/8 bar: %d ticks, beat %d, %d beats", got, m.BeatTicks(5), m.BeatsInBar(5))
	}
	if tick, ok := m.PositionTick(6, 7, 2); !ok || tick != 4*3840+3360+6*480+240 {
		t.Fatalf("@6.7.2 = %d, %t", tick, ok)
	}
	if _, ok := m.PositionTick(6, 8, 1); ok {
		t.Fatal("accepted beat 8 of a 7/8 bar")
	}
	if _, ok := m.PositionTick(6, 1, 3); ok {
		t.Fatal("accepted step 3 of an eighth-note beat")
	}
	if got := m.NextBarLine(28_799); got != 28_800 {
		t.Fatalf("next bar line after 28799 is %d", got)
	}
}

// randomMeterMap builds a map of up to 64 changes over odd and compound meters.
func randomMeterMap(rng *rand.Rand) (MeterMap, []MeterChange) {
	units := []int64{2, 4, 8, 16}
	var changes []MeterChange
	var bar int64
	for n := rng.Intn(64) + 1; len(changes) < n; {
		unit := units[rng.Intn(len(units))]
		beats := rng.Int63n(32) + 1
		if beats*(TicksPerWhole/unit) > MaxBarTicks {
			continue
		}
		changes = append(changes, MeterChange{Bar: bar, Meter: Meter{Beats: beats, Unit: unit}})
		bar += rng.Int63n(9) + 1
	}
	m, err := NewMeterMap(changes)
	if err != nil {
		panic(err)
	}
	return m, changes
}

// referenceBarStart walks the changes bar by bar, independent of the map.
func referenceBarStart(changes []MeterChange, bar int64) int64 {
	var tick int64
	for b := int64(0); b < bar; b++ {
		meter := changes[0].Meter
		for _, c := range changes {
			if c.Bar <= b {
				meter = c.Meter
			}
		}
		tick += meter.Beats * (TicksPerWhole / meter.Unit)
	}
	return tick
}

func TestMeterMapProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(20261110))
	for trial := 0; trial < 200; trial++ {
		m, changes := randomMeterMap(rng)
		last := changes[len(changes)-1].Bar + 8
		for bar := int64(0); bar <= last; bar++ {
			start := m.BarStart(bar)
			if want := referenceBarStart(changes, bar); start != want {
				t.Fatalf("trial %d: bar %d starts at %d, want %d", trial, bar, start, want)
			}
			if m.BarStart(bar+1)-start != m.BarTicks(bar) || m.BarTicks(bar) != m.BeatsInBar(bar)*m.BeatTicks(bar) {
				t.Fatalf("trial %d: bar %d length is inconsistent", trial, bar)
			}
			if b, o := m.BarAt(start); b != bar || o != 0 || !m.IsBarLine(start) {
				t.Fatalf("trial %d: BarAt(BarStart(%d)) = %d, %d", trial, bar, b, o)
			}
			if m.NextBarLine(start) != m.BarStart(bar+1) || m.BarsUntil(start) != bar || m.BarsUntil(start+1) != bar+1 {
				t.Fatalf("trial %d: bar lines around bar %d", trial, bar)
			}
			for count := int64(0); count < 4; count++ {
				length := m.BarsTicks(start+1, count)
				if length != m.BarStart(bar+count)-start {
					t.Fatalf("trial %d: %d bars from bar %d last %d", trial, count, bar, length)
				}
				if n, whole := m.WholeBars(start+1, length); n != count || !whole {
					t.Fatalf("trial %d: WholeBars(%d) = %d, %t; want %d", trial, length, n, whole, count)
				}
				if _, whole := m.WholeBars(start, length+1); whole {
					t.Fatalf("trial %d: %d ticks from bar %d end on a bar line", trial, length+1, bar)
				}
			}
			// Every position in the bar round-trips through ticks.
			beatTicks := m.BeatTicks(bar)
			for beat := int64(1); beat <= m.BeatsInBar(bar); beat++ {
				for step := int64(1); step <= beatTicks/TicksPerStep; step++ {
					tick, ok := m.PositionTick(bar+1, beat, step)
					if !ok {
						t.Fatalf("trial %d: @%d.%d.%d refused", trial, bar+1, beat, step)
					}
					if b, be, s, r := m.Position(tick); b != bar+1 || be != beat || s != step || r != 0 {
						t.Fatalf("trial %d: @%d.%d.%d -> %d -> @%d.%d.%d+%d", trial, bar+1, beat, step, tick, b, be, s, r)
					}
				}
			}
			if _, ok := m.PositionTick(bar+1, m.BeatsInBar(bar)+1, 1); ok {
				t.Fatalf("trial %d: accepted a beat past bar %d", trial, bar+1)
			}
			if _, ok := m.PositionTick(bar+1, 1, beatTicks/TicksPerStep+1); ok {
				t.Fatalf("trial %d: accepted a step past the beat in bar %d", trial, bar+1)
			}
		}
		end := m.BarStart(last)
		for i := 0; i < 500; i++ {
			tick := rng.Int63n(end)
			bar, offset := m.BarAt(tick)
			if offset < 0 || offset >= m.BarTicks(bar) || m.BarStart(bar)+offset != tick {
				t.Fatalf("trial %d: BarAt(%d) = %d, %d", trial, tick, bar, offset)
			}
			next := m.NextBarLine(tick)
			if next <= tick || next != m.BarStart(bar+1) {
				t.Fatalf("trial %d: NextBarLine(%d) = %d", trial, tick, next)
			}
			every := rng.Int63n(8) + 1
			q := m.QuantizeBars(tick, every)
			qb, qo := m.BarAt(q)
			if q < tick || qo != 0 || qb%every != 0 || qb >= every && m.BarStart(qb-every) >= tick {
				t.Fatalf("trial %d: QuantizeBars(%d, %d) = %d", trial, tick, every, q)
			}
			beat := m.QuantizeBeat(tick)
			bb, bo := m.BarAt(beat)
			if beat < tick || beat-tick >= m.BeatTicks(bar) || bo%m.BeatTicks(bb) != 0 {
				t.Fatalf("trial %d: QuantizeBeat(%d) = %d", trial, tick, beat)
			}
			for _, q := range []cmd.Quantize{1, 2, 5, 9} {
				got, err := m.QuantizeTick(tick, q, 16)
				want := beat
				if q > 1 {
					want = m.QuantizeBars(tick, max(1, int64(q)-4))
				}
				if err != nil || got != want {
					t.Fatalf("trial %d: QuantizeTick(%d, %d) = %d, %v; want %d", trial, tick, q, got, err, want)
				}
			}
		}
	}
}

// Ticks map to samples and back through tempo steps placed on the map's bar
// lines, the way the project tempo map and the kernel transport chain clocks.
func TestMeterMapTempoStepsRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	for _, rate := range []int{44100, 48000, 96000} {
		for trial := 0; trial < 40; trial++ {
			m, changes := randomMeterMap(rng)
			last := changes[len(changes)-1].Bar + 8
			clock, err := NewClock(rate, 20_000+rng.Int63n(280_001))
			if err != nil {
				t.Fatal(err)
			}
			clocks := []Clock{clock}
			for bar := rng.Int63n(4) + 1; bar < last; bar += rng.Int63n(6) + 1 {
				if clock, err = clock.Reanchor(m.BarStart(bar), 20_000+rng.Int63n(280_001)); err != nil {
					t.Fatal(err)
				}
				clocks = append(clocks, clock)
			}
			atTick := func(tick int64) Clock {
				c := clocks[0]
				for _, next := range clocks[1:] {
					if next.AnchorTick <= tick {
						c = next
					}
				}
				return c
			}
			atSample := func(sample int64) Clock {
				c := clocks[0]
				for _, next := range clocks[1:] {
					if next.AnchorSample <= sample {
						c = next
					}
				}
				return c
			}
			end := m.BarStart(last)
			previous := int64(-1)
			for tick := int64(0); tick < end; tick += 120 + rng.Int63n(4)*120 {
				sample := atTick(tick).SampleAtTick(tick)
				if sample < previous {
					t.Fatalf("rate %d trial %d: samples decrease at tick %d", rate, trial, tick)
				}
				previous = sample
				if got := atSample(sample).TickAtSample(sample); got != tick {
					t.Fatalf("rate %d trial %d: tick %d -> sample %d -> tick %d", rate, trial, tick, sample, got)
				}
			}
			for _, c := range clocks[1:] {
				if !m.IsBarLine(c.AnchorTick) {
					t.Fatalf("tempo step at %d is not on a bar line", c.AnchorTick)
				}
			}
		}
	}
}

func TestTransportFollowsItsMeterMap(t *testing.T) {
	m, err := NewMeterMap([]MeterChange{{0, Meter{3, 4}}})
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewTransport(48000, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	transport.SetMeterMap(m)
	transport.Play()
	transport.Advance(100)
	if err := transport.QueueTempo(150_000); err != nil {
		t.Fatal(err)
	}
	if _, at := transport.PendingTempo(); at != 2880 {
		t.Fatalf("a queued tempo in 3/4 applies at %d, want 2880", at)
	}
	if err := transport.SeekTick(2881); err != nil {
		t.Fatal(err)
	}
	if _, at := transport.PendingTempo(); at != 5760 {
		t.Fatalf("after a seek the queued tempo applies at %d, want 5760", at)
	}
}

// TestTransportBarMatchesItsMap moves a transport through random maps with
// playback steps, small moves, jumps back and forward and map changes, and
// checks that the bar it keeps between calls always gives the map's answer.
func TestTransportBarMatchesItsMap(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	transport, err := NewTransport(48000, 120_000)
	if err != nil {
		t.Fatal(err)
	}
	transport.Play()
	for round := 0; round < 60; round++ {
		m, _ := randomMeterMap(rng)
		if round%4 == 0 {
			m = MeterMap{}
		}
		transport.SetMeterMap(m)
		for step := 0; step < 500; step++ {
			switch tick := transport.Tick(); rng.Intn(8) {
			case 0:
				err = transport.SeekTick(rng.Int63n(400_000))
			case 1:
				err = transport.SeekTick(max(0, tick-rng.Int63n(10_000)))
			case 2:
				err = transport.SeekTick(tick + rng.Int63n(240))
			default:
				transport.Advance(rng.Intn(4096) + 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			tick := transport.Tick()
			bar, offset := transport.Bar()
			wantBar, wantOffset := m.BarAt(tick)
			if bar != wantBar || offset != wantOffset || transport.NextBarLine() != m.NextBarLine(tick) {
				t.Fatalf("round %d tick %d: transport bar %d+%d next %d, map %d+%d next %d", round, tick, bar, offset, transport.NextBarLine(), wantBar, wantOffset, m.NextBarLine(tick))
			}
		}
	}
	var sink int64
	if allocs := testing.AllocsPerRun(100, func() {
		transport.Advance(64)
		bar, offset := transport.Bar()
		sink += bar + offset + transport.NextBarLine()
	}); allocs != 0 || sink == 0 {
		t.Fatalf("transport bar lookups allocated %.0f times", allocs)
	}
}

func TestMeterMapLookupsAreAllocationFree(t *testing.T) {
	odd, _ := NewMeterMap([]MeterChange{{0, Meter{4, 4}}, {4, Meter{7, 8}}, {8, Meter{3, 4}}, {12, Meter{5, 8}}})
	for _, m := range []MeterMap{{}, odd} {
		var sink int64
		allocs := testing.AllocsPerRun(100, func() {
			for tick := int64(0); tick < 80_000; tick += 997 {
				bar, offset := m.BarAt(tick)
				b, beat, step, rest := m.Position(tick)
				p, _ := m.PositionTick(b, beat, step)
				q, _ := m.QuantizeTick(tick, 6, 16)
				sink += bar + offset + rest + p + q + m.BarStart(bar) + m.BarTicks(bar) + m.BeatTicks(bar) + m.BeatsInBar(bar) +
					m.NextBarLine(tick) + m.BarsUntil(tick) + m.QuantizeBars(tick, 3) + m.QuantizeBeat(tick)
			}
		})
		if allocs != 0 || sink == 0 {
			t.Fatalf("meter map lookups allocated %.0f times", allocs)
		}
	}
}

func BenchmarkMeterMapNextBarLine(b *testing.B) {
	var zero MeterMap
	odd, _ := NewMeterMap([]MeterChange{{0, Meter{4, 4}}, {4, Meter{7, 8}}, {8, Meter{3, 4}}, {12, Meter{5, 8}}})
	for _, c := range []struct {
		name string
		m    MeterMap
	}{{"zero", zero}, {"four-segments", odd}} {
		b.Run(c.name, func(b *testing.B) {
			var sink int64
			for i := 0; i < b.N; i++ {
				sink += c.m.NextBarLine(int64(i) * 128)
			}
			_ = sink
		})
	}
}

// A map that takes the multi-segment paths but is 4/4 throughout must answer
// exactly as the zero map, including negative ticks and bars: the zero map's
// arithmetic is the contract (see MeterMap's domain).
func TestGeneralPathsAgreeWithTheZeroMap(t *testing.T) {
	general, err := NewMeterMap([]MeterChange{{0, Meter{4, 4}}, {7, Meter{4, 4}}, {19, Meter{4, 4}}})
	if err != nil || general.segments == nil {
		t.Fatalf("expected a multi-segment map, got %+v, %v", general, err)
	}
	var zero MeterMap
	ticks := []int64{-1, -239, -240, -959, -960, -3839, -3840, -3841, -7679, -7680, -7681, -20000}
	for tick := int64(-40000); tick <= 120000; tick += 37 {
		ticks = append(ticks, tick)
	}
	for _, tick := range ticks {
		zb, zo := zero.BarAt(tick)
		gb, gofs := general.BarAt(tick)
		if zb != gb || zo != gofs || zero.IsBarLine(tick) != general.IsBarLine(tick) {
			t.Fatalf("BarAt(%d): zero %d,%d general %d,%d", tick, zb, zo, gb, gofs)
		}
		if zero.NextBarLine(tick) != general.NextBarLine(tick) || zero.BarsUntil(tick) != general.BarsUntil(tick) || zero.QuantizeBeat(tick) != general.QuantizeBeat(tick) {
			t.Fatalf("tick %d: next %d/%d, until %d/%d, beat %d/%d", tick, zero.NextBarLine(tick), general.NextBarLine(tick), zero.BarsUntil(tick), general.BarsUntil(tick), zero.QuantizeBeat(tick), general.QuantizeBeat(tick))
		}
		for _, bars := range []int64{1, 2, 3, 4, 16} {
			if zero.QuantizeBars(tick, bars) != general.QuantizeBars(tick, bars) {
				t.Fatalf("QuantizeBars(%d, %d): zero %d general %d", tick, bars, zero.QuantizeBars(tick, bars), general.QuantizeBars(tick, bars))
			}
		}
		zp := [4]int64{}
		gp := [4]int64{}
		zp[0], zp[1], zp[2], zp[3] = zero.Position(tick)
		gp[0], gp[1], gp[2], gp[3] = general.Position(tick)
		if zp != gp {
			t.Fatalf("Position(%d): zero %v general %v", tick, zp, gp)
		}
		for _, length := range []int64{0, 1, 3839, 3840, 3841, 7680, 50000} {
			zn, zw := zero.WholeBars(tick, length)
			gn, gw := general.WholeBars(tick, length)
			if zn != gn || zw != gw {
				t.Fatalf("WholeBars(%d, %d): zero %d,%t general %d,%t", tick, length, zn, zw, gn, gw)
			}
		}
		for _, count := range []int64{0, 1, 2, 5} {
			if zero.BarsTicks(tick, count) != general.BarsTicks(tick, count) {
				t.Fatalf("BarsTicks(%d, %d) differ", tick, count)
			}
		}
	}
	for bar := int64(-30); bar < 40; bar++ {
		if zero.BarStart(bar) != general.BarStart(bar) || zero.BarTicks(bar) != general.BarTicks(bar) || zero.BeatTicks(bar) != general.BeatTicks(bar) || zero.BeatsInBar(bar) != general.BeatsInBar(bar) {
			t.Fatalf("bar %d: start %d/%d", bar, zero.BarStart(bar), general.BarStart(bar))
		}
		for beat := int64(-1); beat < 7; beat++ {
			for step := int64(-1); step < 7; step++ {
				zt, zok := zero.PositionTick(bar, beat, step)
				gt, gok := general.PositionTick(bar, beat, step)
				if zt != gt || zok != gok {
					t.Fatalf("PositionTick(%d,%d,%d): zero %d,%t general %d,%t", bar, beat, step, zt, zok, gt, gok)
				}
			}
		}
	}
	// The reviewer's cases for phase 1.
	if general.QuantizeBars(-1, 1) != 3840 || general.BarsUntil(-3841) != 0 {
		t.Fatalf("negative domain: QuantizeBars(-1,1) = %d, BarsUntil(-3841) = %d", general.QuantizeBars(-1, 1), general.BarsUntil(-3841))
	}
}
