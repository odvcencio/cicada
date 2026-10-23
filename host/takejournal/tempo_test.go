package takejournal

import (
	"m31labs.dev/cicada/musictime"
	"reflect"
	"testing"
)

func TestRecordingTempoSnapshotOwnsAndReopensItsSteps(t *testing.T) {
	s, path := testStore(t)
	timeline := &Timeline{StartTick: 0, EndTick: 4 * 3840, TempoMilli: 120000, TempoChanges: []musictime.TempoChange{{Bar: 1, BPMMilli: 60000}, {Bar: 3, BPMMilli: 137333}}}
	id, err := s.BeginTimeline("vox", "session", Revision([]byte("score")), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]musictime.TempoChange(nil), timeline.TempoChanges...)
	timeline.TempoChanges[0].BPMMilli = 20000
	first, err := s.Get(id)
	if err != nil || !reflect.DeepEqual(first.Timeline.TempoChanges, want) {
		t.Fatal("caller changed retained tempo", err)
	}
	first.Timeline.TempoChanges[0].BPMMilli = 300000
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	restored, err := again.Get(id)
	if err != nil || !reflect.DeepEqual(restored.Timeline.TempoChanges, want) {
		t.Fatal("tempo map lost on reopen", err)
	}
	for _, steps := range [][]musictime.TempoChange{{{Bar: -1, BPMMilli: 120000}}, {{Bar: 0, BPMMilli: 19999}}, {{Bar: 2, BPMMilli: 120000}, {Bar: 1, BPMMilli: 96000}}, {{Bar: 0, BPMMilli: 120000}, {Bar: 0, BPMMilli: 96000}}} {
		timeline.TempoChanges = steps
		if _, err = again.BeginTimeline("vox", "session", Revision([]byte("score")), 48000, 1, timeline); err == nil {
			t.Fatal("accepted malformed recording clock", steps)
		}
	}
}
