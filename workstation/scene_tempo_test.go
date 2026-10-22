package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func tempoProject() *project.Project {
	slow := 60.0
	return &project.Project{
		Title: "Test", TempoMilli: 120000, Key: project.Key{Scale: "minor"},
		Scenes: []project.Scene{{ID: "a"}, {ID: "b", Settings: []project.SceneSetting{{Path: "tempo", Value: project.SceneValue{Unit: "unit", Number: &slow}}}}},
		Song:   []project.SongEntry{{Scene: "a", Bars: 2}, {Scene: "b", Bars: 2}},
	}
}

// The arrangement shows the score tempo, notes a change, and sums the steps.
func TestArrangementSumsTempoSegments(t *testing.T) {
	p := tempoProject()
	html := gosx.RenderHTML((&studioApp{}).arrangement(nil, workspace{Project: p}, "csrf", ""))
	// 2 bars at 120 BPM (4 s) and 2 bars at 60 BPM (8 s) make 0:12.
	for _, want := range []string{"4 bars · 0:12 at 120.0 BPM", "the tempo changes 1 times"} {
		if !strings.Contains(html, want) {
			t.Fatalf("arrangement description lacks %q", want)
		}
	}
	p.Scenes[1].Settings = nil
	html = gosx.RenderHTML((&studioApp{}).arrangement(nil, workspace{Project: p}, "csrf", ""))
	if !strings.Contains(html, "4 bars · 0:08 at 120.0 BPM") || strings.Contains(html, "the tempo changes") {
		t.Fatal("a song with one tempo changed its description")
	}
}
