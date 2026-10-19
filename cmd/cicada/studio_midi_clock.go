package main

import (
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
	if !ok || !f.Playing || f.WallNanos == 0 || f.WallNanos == t.midiFrameWall {
		return
	}
	if f.Bar < 1 || f.Beat < 1 || f.Beat > 4 || f.Tick < 0 || int64(f.Tick) >= seq.PPQ {
		return
	}
	t.midiFrameWall = f.WallNanos
	at := midi.HostTimeNanos()
	tick := (int64(f.Bar)-1)*seq.TicksPerBar + (int64(f.Beat)-1)*seq.PPQ + int64(f.Tick)
	if !t.midiTimeline.Observe(at, tick, int64(f.TempoMilli)) {
		t.resetMIDIClockLocked()
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
