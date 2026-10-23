package midi

import (
	"math"
	"testing"
	"time"
)

func TestScaleHostTimeUsesTimebaseWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		stamp    uint64
		num, den uint32
		want     int64
		ok       bool
	}{
		{24000000, 125, 3, 1000000000, true},
		{1000000000, 1, 1, 1000000000, true},
		{math.MaxUint64, 2, 4, math.MaxInt64, true},
		{math.MaxUint64, 1, 1, 0, false},
		{math.MaxUint64, math.MaxUint32, 1, 0, false},
		{1, 0, 1, 0, false}, {1, 1, 0, 0, false},
	} {
		got, ok := scaleHostTime(tc.stamp, tc.num, tc.den)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("scale(%d,%d,%d)=%d,%v, want %d,%v", tc.stamp, tc.num, tc.den, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTimelinePreservesDelayedPacketTimingAndTempo(t *testing.T) {
	const origin = int64(time.Second)
	var clock Timeline
	clock.Reset(origin)
	clock.Observe(origin, 3840, 120000)
	clock.Observe(origin+int64(500*time.Millisecond), 4800, 60000)
	// These events arrive after the tempo change, but their source times
	// still select the appropriate earlier transport observation.
	for _, tc := range []struct{ at, want int64 }{
		{origin + int64(100*time.Millisecond), 4032},
		{origin + int64(200*time.Millisecond), 4224},
		{origin + int64(600*time.Millisecond), 4896},
	} {
		got := clock.PositionAt(tc.at, true)
		if !got.Valid || got.Tick != tc.want || got.Epoch != 1 {
			t.Fatalf("position=%+v, want tick %d", got, tc.want)
		}
	}
	packetTime := origin + int64(650*time.Millisecond)
	first := clock.PositionAt(packetTime, true)
	clock.Observe(packetTime, 5000, 120000)
	if next := clock.PositionAt(packetTime, true); next != first {
		t.Fatalf("same packet spread over different ticks: %+v / %+v", first, next)
	}
}

func TestTimelineRejectsPreviousPlaybackAndBoundsMissingClock(t *testing.T) {
	const origin = int64(time.Second)
	var clock Timeline
	clock.Reset(origin)
	clock.Observe(origin, 960, 120000)
	if clock.PositionAt(origin+int64(251*time.Millisecond), true).Valid {
		t.Fatal("extrapolated a missing clock beyond 250ms")
	}
	if clock.Observe(origin+1, 0, 120000) {
		t.Fatal("backwards transport did not request a new epoch")
	}
	clock.Reset(origin + 2)
	if p := clock.PositionAt(origin+1, true); !p.Stale || p.Valid || p.Epoch != 2 {
		t.Fatalf("old event admitted after restart: %+v", p)
	}
	for i := range 100 {
		clock.Observe(origin+3+int64(i)*int64(40*time.Millisecond), int64(i)*80, 120000)
	}
	if clock.count != len(clock.frames) {
		t.Fatal("history is not bounded")
	}
	if clock.PositionAt(origin+3, true).Valid {
		t.Fatal("evicted history invented a position")
	}
}
