package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestSessionLaunchMatrixUsesScoreTiming(t *testing.T) {
	p := &project.Project{Tracks: []project.Track{{ID: "bass"}}, Scenes: []project.Scene{{ID: "main", Bindings: map[string]string{"bass": "pulse"}}}}
	html := gosx.RenderHTML((&studioApp{}).session(nil, workspace{Project: p}, "token", ""))
	if !strings.Contains(html, "Launches use the score timing") || strings.Contains(html, `name="quantize"`) {
		t.Fatalf("session launch matrix does not use score timing: %s", html)
	}
}
