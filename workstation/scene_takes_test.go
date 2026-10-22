package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestSceneTakeControlsRetainRecordingContext(t *testing.T) {
	v := workspace{Revision: "saved", Project: &project.Project{Tracks: []project.Track{{ID: "guitar", Kind: "audio"}}, Scenes: []project.Scene{{ID: "verse", Bindings: map[string]string{"guitar": "selected-clip"}}}}}
	pass := func(id, scene, track string) takejournal.Take {
		return takejournal.Take{ID: id, Scene: scene, Track: track, Frames: 24000, Rate: 48000, Asset: project.Asset{Path: "audio/" + id + ".wav"}}
	}
	takes := []takejournal.Take{pass("ready", "verse", "guitar"), pass("selected", "verse", "guitar"), pass("missing", "removed", "guitar"), pass("other", "verse", "voice")}
	timeline := pass("timeline", "verse", "guitar")
	timeline.Timeline = &takejournal.Timeline{}
	takes = append(takes, timeline)
	s := &studioApp{}
	page := gosx.RenderHTML(s.sceneTakes(v, "csrf", "guitar", takes, false))
	for _, text := range []string{"Scene takes", "Scene take 1", "0.50 seconds", "Use in verse", "In verse", "Scene no longer exists", `name="takeId" value="ready"`, `aria-label="Preview Scene take 1"`} {
		if !strings.Contains(page, text) {
			t.Errorf("scene-take view is missing %s", text)
		}
	}
	if strings.Contains(page, `data-scene-take="other"`) || strings.Contains(page, `data-scene-take="timeline"`) {
		t.Fatal("scene takes crossed the selected track or included timeline takes")
	}
	busy := gosx.RenderHTML(s.sceneTakes(v, "csrf", "guitar", takes[:1], true))
	if !strings.Contains(busy, `disabled`) {
		t.Fatal("selection remained available during capture")
	}
	v.Project.Tracks = nil
	missingTrack := gosx.RenderHTML(s.sceneTakes(v, "csrf", "", takes[:1], false))
	if !strings.Contains(missingTrack, "Audio track no longer exists") || !strings.Contains(missingTrack, `<audio`) {
		t.Fatal("a deleted track hid its recording or offered an invalid selection")
	}
}
