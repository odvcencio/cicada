package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func TestSessionLaunchMatrixUsesScoreTiming(t *testing.T) {
	p := &project.Project{Tracks: []project.Track{{ID: "bass"}}, Scenes: []project.Scene{{ID: "main", Bindings: map[string]string{"bass": "pulse"}}}}
	html := gosx.RenderHTML((&studioApp{}).session(nil, workspace{Project: p, Revision: "rev"}, "token", ""))
	if !strings.Contains(html, "Launches use the score timing") || strings.Contains(html, `name="quantize"`) {
		t.Fatalf("session launch matrix does not use score timing: %s", html)
	}
	// The renderer takes the revision and the CSRF token as adjacent strings;
	// pin which one each launch form posts.
	for _, want := range []string{`name="revision" value="rev"`, `name="csrf_token" value="token"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("launch form is missing %s: %s", want, html)
		}
	}
}

func TestSessionWithoutScenesKeepsArrangement(t *testing.T) {
	s := &studioApp{}
	v := workspace{Project: &project.Project{}}
	got := gosx.RenderHTML(s.session(nil, v, "token", ""))
	want := gosx.RenderHTML(s.arrangement(nil, v, "token", ""))
	if got != want {
		t.Fatal("a project without scenes should show only its arrangement")
	}
	if got := gosx.RenderHTML(s.session(nil, workspace{}, "token", "")); !strings.Contains(got, "save a valid score to open the session") {
		t.Fatalf("session without a project did not explain the failure: %s", got)
	}
}
