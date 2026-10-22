package midi

import (
	"testing"
	"time"

	"m31labs.dev/cicada/musictime"
)

func TestTimelineCrossesTempoBeforeTheNextObservation(t *testing.T) {
	const origin = int64(time.Second)
	var clock Timeline
	clock.Reset(origin)
	step := musictime.ObservedTempoStep{AfterNanos: int64(100 * time.Millisecond), Tick: 3840, BPMMilli: 60000}
	clock.ObserveClock(origin, 3648, 120000, step)
	for _, tc := range []struct {
		at   time.Duration
		want int64
	}{{50 * time.Millisecond, 3744}, {100 * time.Millisecond, 3840}, {200 * time.Millisecond, 3936}, {250 * time.Millisecond, 3984}} {
		p := clock.PositionAt(origin+int64(tc.at), true)
		if !p.Valid || p.Tick != tc.want {
			t.Fatalf("at %s: %+v, want %d", tc.at, p, tc.want)
		}
	}
	if clock.PositionAt(origin+int64(251*time.Millisecond), true).Valid {
		t.Fatal("tempo forecast extended the freshness bound")
	}
	clock.Observe(origin+int64(150*time.Millisecond), 3888, 60000)
	if p := clock.PositionAt(origin+int64(125*time.Millisecond), true); !p.Valid || p.Tick != 3864 {
		t.Fatalf("delayed event lost its earlier boundary: %+v", p)
	}
	at := origin + int64(225*time.Millisecond)
	before := clock.PositionAt(at, true)
	clock.Observe(at, 4000, 180000)
	if after := clock.PositionAt(at, true); before != after {
		t.Fatal("one packet changed position during delivery", before, after)
	}
	clock.Reset(at + 1)
	if p := clock.PositionAt(at, true); !p.Stale || p.Valid {
		t.Fatal("old forecast entered new epoch", p)
	}
}
