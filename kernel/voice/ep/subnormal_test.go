package ep

import (
	"math"
	"os"
	"testing"
	"time"
)

// heldChord is the chord of the held-chord measurement: C4, E4, G4 and B4,
// struck at velocity 100 and held.
var heldChord = [...]uint8{60, 64, 67, 71}

func newHeldChord(t testing.TB, rate int) *Instrument {
	t.Helper()
	p, err := Patch("tine_ep")
	if err != nil {
		t.Fatal(err)
	}
	i, err := New(rate, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range heldChord {
		if err := i.NoteOn(note, 100); err != nil {
			t.Fatal(err)
		}
	}
	return i
}

// isSubnormal reports whether x is a nonzero float32 with a zero exponent.
func isSubnormal(x float32) bool {
	bits := math.Float32bits(x)
	return bits&0x7f800000 == 0 && bits&0x007fffff != 0
}

// subnormalStates counts the recursive state values that hold a subnormal
// number, by name.
func (i *Instrument) subnormalStates() map[string]int {
	found := map[string]int{}
	note := func(name string, x float32) {
		if isSubnormal(x) {
			found[name]++
		}
	}
	for n := range i.voices {
		v := &i.voices[n]
		if !v.active {
			continue
		}
		for m := range v.modes {
			note("mode q", v.modes[m].q)
			note("mode p", v.modes[m].p)
		}
		note("thump", v.thump)
		note("thumpP", v.thumpP)
		note("click", v.click)
		note("lastField", v.lastField)
	}
	for name, x := range map[string]float32{"low": i.low, "dcIn": i.dcIn, "dcOut": i.dcOut, "stealL": i.stealL, "stealR": i.stealR, "lfoQ": i.lfoQ, "lfoP": i.lfoP} {
		note(name, x)
	}
	return found
}

// A held chord and its release must never leave recursive state in subnormal
// numbers, which most processors handle many times slower than normal ones.
func TestHeldChordStateIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	i := newHeldChord(t, rate)
	first := map[string]float64{}
	check := func(second float64) {
		for name := range i.subnormalStates() {
			if _, seen := first[name]; !seen {
				first[name] = second
			}
		}
	}
	for frame := range 16 * rate {
		i.NextStereo()
		check(float64(frame) / rate)
	}
	i.AllNotesOff()
	for frame := range 30 * rate {
		i.NextStereo()
		check(16 + float64(frame)/rate)
	}
	for name, second := range first {
		t.Errorf("%s is subnormal from %.2f s", name, second)
	}
}

// heldChordSeconds renders seconds of the held chord and returns the wall
// time each second of audio took.
func heldChordSeconds(t testing.TB, seconds int) []time.Duration {
	const rate = 48000
	i := newHeldChord(t, rate)
	costs := make([]time.Duration, seconds)
	for s := range costs {
		started := time.Now()
		for range rate {
			i.NextStereo()
		}
		costs[s] = time.Since(started)
	}
	return costs
}

// BenchmarkHeldChord renders a held four-note chord for 16 seconds and reports
// the cost of the first and of the last second of audio, in milliseconds of
// CPU per second of audio, and their ratio. A flat cost has a ratio near 1.
func BenchmarkHeldChord(b *testing.B) {
	var first, last time.Duration
	for range b.N {
		costs := heldChordSeconds(b, 16)
		first += costs[0]
		last += costs[len(costs)-1]
	}
	b.ReportMetric(float64(first.Microseconds())/1000/float64(b.N), "ms/first-s")
	b.ReportMetric(float64(last.Microseconds())/1000/float64(b.N), "ms/last-s")
	b.ReportMetric(float64(last)/float64(first), "last/first")
}

// TestHeldChordCostPerSecond logs the cost of each second of a held chord. It
// is a measurement, not a gate; set CICADA_EP_COST=1 to run it.
func TestHeldChordCostPerSecond(t *testing.T) {
	if testing.Short() || !costEnabled() {
		t.Skip("set CICADA_EP_COST=1 to measure")
	}
	for s, cost := range heldChordSeconds(t, 16) {
		t.Logf("second %2d: %6.2f ms of CPU per second of audio", s+1, float64(cost.Microseconds())/1000)
	}
}

func costEnabled() bool { return os.Getenv("CICADA_EP_COST") == "1" }
