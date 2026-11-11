package ep

import (
	"os"
	"testing"
	"time"

	"m31labs.dev/cicada/kernel/dsp/subnormal/subnormaltest"
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

// pinnedHeldChordHash is the float32 output of TestHeldChordStateIsNeverSubnormal
// (16 s held, 30 s released, 48 kHz), produced alike on amd64 and arm64.
const pinnedHeldChordHash = 0x5e0add3af9bcc789

// A held chord and its release must never leave recursive state in subnormal
// numbers, which most processors handle many times slower than normal ones.
// Before the flush the click was subnormal from 0.12 s, the modes from 0.89 s,
// and the output filters after the release. The output is pinned: the
// voice goldens end too soon after a release to see what a flush changes.
func TestHeldChordStateIsNeverSubnormal(t *testing.T) {
	const rate = 48000
	i := newHeldChord(t, rate)
	hash := subnormaltest.NewHash(rate)
	for frame := range 46 * rate {
		if frame == 16*rate {
			i.AllNotesOff()
		}
		hash.Add(i.NextStereo())
		// The state is swept every flushFrames samples; between sweeps a value
		// may sit in subnormal numbers for those few samples.
		if i.frames%(64*flushFrames) == 0 {
			if found := subnormaltest.Find(i, 5); len(found) > 0 {
				t.Fatalf("after the sweep at %.2f s the state holds subnormal numbers: %v", float64(frame)/rate, found)
			}
		}
	}
	subnormaltest.CheckPin(t, "ep held chord", hash, pinnedHeldChordHash)
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
