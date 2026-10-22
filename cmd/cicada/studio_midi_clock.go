package main

import (
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/kernel/seq"
)

func (t *studioTransport) resetMIDIClockLocked() {
	t.midiTimeline.Reset(midi.HostTimeNanos())
	t.midiFrameWall = 0
}

func (t *studioTransport) observeMIDIClockLocked() {
	if !t.playing || t.stream == nil {
		return
	}
	f, ok := t.telemetry.Transport()
	if !ok {
		return
	}
	t.observeMIDIFrameLocked(f, time.Now().UnixNano(), midi.HostTimeNanos())
}

func (t *studioTransport) observeMIDIFrameLocked(f liveplay.TransportFrame, wallNow, hostNow int64) {
	if !t.playing || !f.Playing || f.WallNanos == t.midiFrameWall || f.Bar < 1 || f.Beat < 1 || f.Beat > 4 || f.Tick < 0 || int64(f.Tick) >= seq.PPQ || f.TempoMilli < 20000 || f.TempoMilli > 300000 {
		return
	}
	at, valid := midi.ObservationHostTime(f.WallNanos, wallNow, hostNow)
	if !valid {
		return
	}
	t.midiFrameWall = f.WallNanos
	tick := (int64(f.Bar)-1)*seq.TicksPerBar + (int64(f.Beat)-1)*seq.PPQ + int64(f.Tick)
	if !t.midiTimeline.Observe(at, tick, int64(f.TempoMilli)) {
		t.midiTimeline.Reset(at)
		t.midiFrameWall = f.WallNanos
		t.midiTimeline.Observe(at, tick, int64(f.TempoMilli))
	}
}

func (t *studioTransport) midiPosition(event midi.Event) *midi.Position {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observeMIDIClockLocked()
	position := t.midiTimeline.PositionAt(event.HostNanos, t.playing)
	return &position
}
