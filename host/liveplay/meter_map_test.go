package liveplay

import (
	"sync"
	"testing"

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
