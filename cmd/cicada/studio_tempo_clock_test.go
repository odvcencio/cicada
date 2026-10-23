package main

import (
	"encoding/json"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/musictime"
)

func TestMIDIObservationAndProjectionShareTheRenderedTempoStep(t *testing.T) {
	const origin = int64(time.Hour)
	wall := time.Now().Add(-80 * time.Millisecond).UnixNano()
	frame := liveplay.TransportFrame{WallNanos: wall, Bar: 1, Beat: 4, Tick: 768, TempoMilli: 120000, Playing: true}
	step := musictime.ObservedTempoStep{AfterNanos: int64(100 * time.Millisecond), Tick: 3840, BPMMilli: 60000}
	tpt := newStudioTransport("")
	tpt.playing = true
	tpt.midiTimeline.Reset(origin)
	tpt.telemetry.PublishTransportClock(frame, step)
	f, next, ok := tpt.telemetry.TransportClock()
	if !ok {
		t.Fatal("missing clock")
	}
	tpt.observeMIDIFrameLocked(f, next, wall+int64(80*time.Millisecond), origin+int64(80*time.Millisecond))
	if p := tpt.midiTimeline.PositionAt(origin+int64(200*time.Millisecond), true); !p.Valid || p.Tick != 3936 {
		t.Fatal("native observation ignored tempo step", p)
	}
	data, err := json.Marshal(tpt.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		NextTempo musictime.ObservedTempoStep `json:"nextTempo"`
	}
	if json.Unmarshal(data, &state) != nil || state.NextTempo != step {
		t.Fatal("projection lost matching tempo step", string(data))
	}
	// Legacy publication must clear metadata; it cannot borrow an older step.
	tpt.telemetry.PublishTransport(frame)
	if state := tpt.snapshot(); state.NextTempo != nil {
		t.Fatal("forecast leaked into an unrelated observation")
	}
}
