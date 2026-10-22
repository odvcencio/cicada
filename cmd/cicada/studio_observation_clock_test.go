package main

import (
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/musictime"
)

func TestMIDIObservationUsesRenderTimeAcrossDelayedPollingAndWrap(t *testing.T) {
	const wall = int64(1_700_000_000_000_000_000)
	const host = int64(time.Hour)
	tpt := newStudioTransport("")
	tpt.playing = true
	tpt.midiTimeline.Reset(host - 1)
	f := liveplay.TransportFrame{WallNanos: wall, Bar: 2, Beat: 1, TempoMilli: 120000, Playing: true}
	tpt.observeMIDIFrameLocked(f, musictime.ObservedTempoStep{}, wall+int64(120*time.Millisecond), host+int64(120*time.Millisecond))
	if p := tpt.midiTimeline.PositionAt(host+int64(100*time.Millisecond), true); !p.Valid || p.Tick != 4032 {
		t.Fatalf("poll delay shifted queued input: %+v", p)
	}
	tpt.observeMIDIFrameLocked(f, musictime.ObservedTempoStep{}, wall+int64(200*time.Millisecond), host+int64(200*time.Millisecond))
	if p := tpt.midiTimeline.PositionAt(host+int64(150*time.Millisecond), true); !p.Valid || p.Tick != 4128 {
		t.Fatalf("republishing moved the observation: %+v", p)
	}
	// Playback wrapped before the delayed observation arrived. Input after
	// that observation belongs to the new epoch, rather than being discarded
	// because its source time predates the polling call.
	f.WallNanos += int64(200 * time.Millisecond)
	f.Bar, f.TempoMilli = 1, 60000
	tpt.observeMIDIFrameLocked(f, musictime.ObservedTempoStep{}, wall+int64(240*time.Millisecond), host+int64(240*time.Millisecond))
	if p := tpt.midiTimeline.PositionAt(host+int64(220*time.Millisecond), true); !p.Valid || p.Stale || p.Epoch != 2 || p.Tick != 19 {
		t.Fatalf("wrapped source event lost its epoch: %+v", p)
	}
	f.WallNanos += int64(10 * time.Millisecond)
	tpt.observeMIDIFrameLocked(f, musictime.ObservedTempoStep{}, wall+int64(time.Second), host+int64(time.Second))
	if p := tpt.midiTimeline.PositionAt(host+int64(time.Second), true); p.Valid {
		t.Fatal("stale render was accepted as a fresh clock")
	}
}

func TestTransportProjectionReportsTheOriginalFrameAge(t *testing.T) {
	tpt := newStudioTransport("")
	observed := time.Now().Add(-80 * time.Millisecond).UnixNano()
	tpt.telemetry.PublishTransport(liveplay.TransportFrame{WallNanos: observed, Bar: 2, Beat: 1, TempoMilli: 120000, Playing: true})
	state := tpt.snapshot()
	if state.FrameAgeNanos == nil || *state.FrameAgeNanos < int64(80*time.Millisecond) {
		t.Fatalf("projection forgot render age: %+v", state.FrameAgeNanos)
	}
	tpt.telemetry.PublishTransport(liveplay.TransportFrame{WallNanos: time.Now().Add(time.Hour).UnixNano(), Bar: 2, Beat: 1, TempoMilli: 120000, Playing: true})
	if age := tpt.snapshot().FrameAgeNanos; age == nil || *age != -1 {
		t.Fatal("future render timestamp was extrapolated")
	}
}
