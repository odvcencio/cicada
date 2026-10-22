package control

import (
	"math"
	"testing"
	"time"
)

func TestRecordingClockIncludesRenderAgeAndEstimatedTransit(t *testing.T) {
	age := int64(80 * time.Millisecond)
	s := PlaybackState{Bar: 2, Step: 1, Playing: true, FrameAgeNanos: &age, RoundTrip: 40 * time.Millisecond}
	p := s.Playhead()
	if got := p.NoteTick(25*time.Millisecond, 120); got != 4080 { // 125 ms from the original sample.
		t.Fatalf("recording ignored source age: %d", got)
	}
	if p.Age != 100*time.Millisecond || !p.ClockFresh(100*time.Millisecond, time.Second) {
		t.Fatal("render age and response transit were not retained")
	}
	if got := p.NoteTick(time.Hour, 120); got != 4320 {
		t.Fatal("recording exceeded the total 250 ms bound", got)
	}
	for _, bad := range []int64{-1, int64(2 * time.Second), math.MaxInt64} {
		s.FrameAgeNanos = &bad
		p := s.Playhead()
		if p.ClockFresh(0, time.Second) || p.NoteTick(time.Hour, 120) > 4320 {
			t.Fatal("invalid or stale clock appeared fresh", bad, p)
		}
	}
	s.FrameAgeNanos = nil // Older hosts do not advertise render age.
	if got := s.Playhead().NoteTick(25*time.Millisecond, 120); got != 3888 {
		t.Fatal("legacy fallback clock changed", got)
	}
	s.Playing, s.FrameAgeNanos = false, &age
	if p := s.Playhead(); !p.ClockFresh(time.Hour, time.Second) || p.NoteTick(time.Hour, 120) != 3840 {
		t.Fatal("a stopped position became stale or advanced", p)
	}
}
