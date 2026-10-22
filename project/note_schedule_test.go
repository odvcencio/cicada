package project

import (
	"bytes"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func TestNoteSchedulePreservesPlaybackTimingWithoutPCM(t *testing.T) {
	_, _, score := assetFixture(t)
	p, diagnostics := FromScore(score)
	if p == nil || hasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	before, _ := CanonicalJSON(p)
	pcm := make([]float32, 4800)
	playback, err := CompileEngineWithAssets(p, 48000, 128, []engine.AudioAsset{{Left: pcm, SampleRate: 48000}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.New(playback); err != nil {
		t.Fatal("reference playback configuration is invalid", err)
	}
	notes, err := CompileNoteSchedule(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(notes.Patterns, playback.Patterns) || !reflect.DeepEqual(notes.Scenes, playback.Scenes) || !reflect.DeepEqual(notes.Song, playback.Song) || !reflect.DeepEqual(notes.Schedule, playback.Schedule) {
		t.Fatal("interchange uses different timing than playback")
	}
	for i, kind := range notes.TrackKinds {
		if kind != playback.Track[i].Kind {
			t.Fatal("interchange changed instrument identity")
		}
	}
	after, _ := CanonicalJSON(p)
	if !bytes.Equal(before, after) {
		t.Fatal("note schedule mutated the project")
	}
	if _, err := CompileEngine(p, 48000, 128); err == nil {
		t.Fatal("playback accepted unprepared PCM")
	}
	p.Clips[0].EndFrame = p.Assets[0].Frames + 1
	if _, err := CompileNoteSchedule(p); err == nil {
		t.Fatal("note export bypassed clip metadata validation")
	}
}
