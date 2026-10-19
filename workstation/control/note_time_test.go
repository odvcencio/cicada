package control

import (
	"encoding/json"
	"testing"
	"time"

	"m31labs.dev/cicada/host/midi"
)

func TestDelayedNativeChordKeepsSourcePositionAndDuration(t *testing.T) {
	const origin = int64(time.Second)
	var clock midi.Timeline
	clock.Reset(origin)
	for i := range 20 {
		clock.Observe(origin+int64(i)*int64(40*time.Millisecond), 3840+int64(i)*77, 120000)
	}
	var take Recording
	take.Track, take.Pattern = "keys", "phrase"
	for i := range 17 {
		at := origin + int64(100*time.Millisecond)
		p := clock.PositionAt(at, true)
		on, ok := NativeNoteTime(midi.Event{HostNanos: at, TimeSource: "source", Position: &p}, clock.Epoch(), 9000+int64(i)*500)
		if !ok {
			t.Fatal("valid delayed input rejected")
		}
		n := Note{Tick: on.Tick, EndTick: on.Tick, Note: 60 + i, Velocity: 100, Input: on.Input}
		at += int64(200 * time.Millisecond)
		p = clock.PositionAt(at, true)
		off, _ := NativeNoteTime(midi.Event{HostNanos: at, TimeSource: "source", Position: &p}, clock.Epoch(), 16000+int64(i)*500)
		n.End(off)
		if i > 0 && (n.Tick != take.Notes[0].Tick || n.EndTick != take.Notes[0].EndTick) {
			t.Fatal("UI delivery spread a simultaneous chord")
		}
		if n.Input.EndHostNanos-n.Input.HostNanos != int64(200*time.Millisecond) || n.Input.Estimated {
			t.Fatalf("source duration changed: %+v", n.Input)
		}
		take.Notes = append(take.Notes, n)
	}
	data, _ := json.Marshal([]Recording{take})
	var restored []Recording
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRecordings(restored); err != nil {
		t.Fatal(err)
	}
	if restored[0].Notes[0].Input.HostNanos != origin+int64(100*time.Millisecond) {
		t.Fatal("draft lost raw input time")
	}
}

func TestNativeTimingRejectsOldEpochAndMarksForcedRelease(t *testing.T) {
	event := midi.Event{HostNanos: 1000, TimeSource: "arrival", Position: &midi.Position{Epoch: 2, Tick: 960, Valid: true, Playing: true}}
	for _, tc := range []struct {
		epoch          uint64
		stale, playing bool
	}{{3, false, true}, {2, true, true}, {2, false, false}} {
		event.Position.Stale, event.Position.Playing = tc.stale, tc.playing
		if _, ok := NativeNoteTime(event, tc.epoch, 4000); ok {
			t.Fatal("admitted input from stopped or old playback")
		}
	}
	event.Position.Stale, event.Position.Playing = false, true
	on, _ := NativeNoteTime(event, 2, 4000)
	n := Note{Tick: on.Tick, EndTick: on.Tick, Input: on.Input}
	n.End(NoteTime{Tick: 1200})
	if n.Input.EndHostNanos != 0 || !n.Input.Estimated || n.EndTick != 1200 {
		t.Fatal("forced release invented a source time")
	}
	event.Position.Valid = false
	if fallback, ok := NativeNoteTime(event, 2, 1500); !ok || fallback.Tick != 1500 || !fallback.Input.Estimated {
		t.Fatal("unmapped input silently lost source time or implied exact timing")
	}
}
