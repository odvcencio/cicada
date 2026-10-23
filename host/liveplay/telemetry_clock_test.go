package liveplay

import (
	"io"
	"testing"
	"time"
)

func TestTelemetryRepublicationRetainsTheRenderClock(t *testing.T) {
	p, err := New(meterScore(t, "clock", -12), 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	before := time.Now().UnixNano()
	if _, err := io.CopyN(io.Discard, p, 128*8); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixNano()
	p.PublishTelemetry()
	first, ok := pub.Transport()
	if !ok || first.WallNanos < before || first.WallNanos > after {
		t.Fatalf("frame time is not the render observation: %+v", first)
	}
	time.Sleep(2 * time.Millisecond)
	// A control update that has not reached the renderer must not rewrite the
	// last sample's tempo, nor make its original observation look fresh.
	p.tempoMilli.Store(240000)
	p.PublishTelemetry()
	second, ok := pub.Transport()
	if !ok || first.WallNanos != second.WallNanos || first.TempoMilli != second.TempoMilli || first.SampleFrame != second.SampleFrame {
		t.Fatalf("publication changed the recorded clock: %+v -> %+v", first, second)
	}
}
