package main

import (
	"testing"
	"time"

	"m31labs.dev/cicada/host/midi"
)

func TestNativeMIDIClockRejectsQueuedInputAfterStop(t *testing.T) {
	transport := newStudioTransport("")
	transport.playing = true
	transport.resetMIDIClockLocked()
	at := midi.HostTimeNanos()
	if at == 0 {
		t.Skip("native MIDI clock unavailable")
	}
	transport.midiTimeline.Observe(at, 3840, 120000)
	event := midi.Event{HostNanos: at}
	before := transport.midiPosition(event)
	if !before.Valid || before.Tick != 3840 || !before.Playing {
		t.Fatalf("native input not mapped: %+v", before)
	}
	// The event remains queued while playback stops and starts a new take.
	time.Sleep(time.Microsecond)
	transport.pause()
	transport.mu.Lock()
	transport.playing = true
	transport.resetMIDIClockLocked()
	transport.mu.Unlock()
	after := transport.midiPosition(event)
	if !after.Stale || after.Valid || after.Epoch <= before.Epoch {
		t.Fatalf("queued event entered new playback: %+v -> %+v", before, after)
	}
}
