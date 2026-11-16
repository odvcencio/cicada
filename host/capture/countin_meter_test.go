package capture

import (
	"testing"

	"m31labs.dev/cicada/kernel/seq"
)

// A count-in in any meter the language allows fits: 6/8 for 8 bars, 12/8 for
// 3 bars and 5/4 for 7 bars once indexed past 32 clicks (#358 review), and the
// largest, 32 beats for 8 bars, is 256 clicks.
func TestCountInFitsEveryMeter(t *testing.T) {
	for _, c := range []struct {
		beats, unit int64
		bars, want  int
	}{
		{6, 8, 8, 48}, {12, 8, 3, 36}, {5, 4, 7, 35}, {3, 4, 8, 24}, {32, 4, 8, 256},
	} {
		m, err := seq.NewMeterMap([]seq.MeterChange{{Meter: seq.Meter{Beats: c.beats, Unit: c.unit}}})
		if err != nil {
			t.Fatal(err)
		}
		count, err := NewCountIn(m, 48000, 120000, c.bars)
		if err != nil || count.count != c.want || count.perBar != int(c.beats) {
			t.Fatalf("%d/%d for %d bars: %d clicks, %v", c.beats, c.unit, c.bars, count.count, err)
		}
		// The last click sits one beat before the count-in ends.
		beat := m.BeatTicks(0)
		clock, _ := seq.NewClock(48000, 120000)
		if last := count.beats[c.want-1]; last != clock.SampleAtTick(int64(c.want-1)*beat) || count.Frames != clock.SampleAtTick(int64(c.want)*beat) {
			t.Fatalf("%d/%d: last click %d, end %d", c.beats, c.unit, last, count.Frames)
		}
		out := [][]float32{make([]float32, count.Frames), make([]float32, count.Frames)}
		count.click(out, 0, int(count.Frames))
	}
}
