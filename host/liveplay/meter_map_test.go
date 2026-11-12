package liveplay

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// TestPositionCellNeverMixesScores has one writer publish two scores in turn,
// each with its own position and meter map, while the test reads. Every read
// must pair a position with the map that placed it: a 3/4 score's bar and step
// decoded on a 4/4 map is a wrong tick.
func TestPositionCellNeverMixesScores(t *testing.T) {
	waltz, err := seq.NewMeterMap([]seq.MeterChange{{Bar: 0, Meter: seq.Meter{Beats: 3, Unit: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	fourFour := &seq.MeterMap{}
	const waltzPosition, fourFourPosition = 9<<8 | 1, 7<<8 | 5
	var c positionCell
	c.store(fourFourPosition, fourFour)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				c.store(waltzPosition, &waltz)
			} else {
				c.store(fourFourPosition, fourFour)
			}
		}
	}()
	seen := map[uint64]int{}
	for range 200_000 {
		position, meterMap := c.load()
		if (meterMap == &waltz) != (position == waltzPosition) {
			close(stop)
			wg.Wait()
			t.Fatalf("read position %#x with the other score's map", position)
		}
		seen[position]++
	}
	close(stop)
	wg.Wait()
	if len(seen) != 2 {
		t.Logf("the reader saw only %v; the writer may not have overlapped it", seen)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		c.store(waltzPosition, &waltz)
		c.load()
	}); allocs != 0 {
		t.Fatalf("publishing a position allocated %.0f times", allocs)
	}
}

// meterTestScore is an empty one-track score at 120 BPM in beats/unit.
func meterTestScore(t *testing.T, name string, beats, unit int64) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
	cfg.Track[0].Kind = engine.VoiceAcid
	if beats != 4 || unit != 4 {
		cfg.Meters = []seq.MeterChange{{Meter: seq.Meter{Beats: beats, Unit: unit}}}
	}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: name}
}

// A 4/4 song swapped for an edit in 3/4 publishes the new map with positions
// placed on it, and a reader on another goroutine never sees a position paired
// with a map it does not fit. At 120 BPM a 4/4 bar is 96,000 frames and a 3/4
// bar 72,000.
func TestSwapToAnotherMeterKeepsPositionAndMapTogether(t *testing.T) {
	p, err := New(meterTestScore(t, "four", 4, 4), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, m := p.PositionWithMap(); m.BarTicks(0) != 3840 {
		t.Fatalf("the first score's map has %d-tick bars", m.BarTicks(0))
	}
	// Into bar 2, then offer the 3/4 edit: it lands where bar 3 begins.
	if _, err := io.CopyN(io.Discard, p, (96_000+blockFrames)*8); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(meterTestScore(t, "three", 3, 4)); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var mixed atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			position, m := p.PositionWithMap()
			// The 3/4 map exists only from bar 3, where the edit lands.
			if position.Bar < 1 || position.Step < 1 || position.Step*seq.TicksPerStep > m.BarTicks(position.Bar-1) || m.BarTicks(0) == 2880 && position.Bar < 3 {
				mixed.Store(position)
			}
		}
	}()
	// Stop at the end of the block that holds the swap: frame 192,000 begins
	// block 750, so the position read now is the one that block published.
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	position, m := p.PositionWithMap()
	stop.Store(true)
	wg.Wait()
	if bad := mixed.Load(); bad != nil {
		t.Fatalf("a reader saw %+v with a map it does not fit", bad)
	}
	if m.BarTicks(0) != 2880 || p.MeterMap().BarTicks(0) != 2880 || position.Bar != 3 || position.Step != 1 {
		t.Fatalf("after the swap: bar %d with %d-tick bars", position.Bar, m.BarTicks(0))
	}
	// One 3/4 bar later the position is bar 4; on 4/4 it would still be bar 3.
	if _, err := io.CopyN(io.Discard, p, 72_000*8); err != nil {
		t.Fatal(err)
	}
	if position, m := p.PositionWithMap(); position.Bar != 4 || m.BarTicks(0) != 2880 {
		t.Fatalf("one 3/4 bar after the swap: bar %d step %d", position.Bar, position.Step)
	}
}
