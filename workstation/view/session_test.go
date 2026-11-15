package view

import (
	"os"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

// This fixture was captured from studioApp.session before the matrix was
// extracted. It covers form data, escaping, ordering, empty/keep and stop slots.
func TestSceneLaunchMatrixPreservesSessionHTML(t *testing.T) {
	p := &project.Project{
		Tracks: []project.Track{{ID: `bass<&"`}, {ID: "drums"}, {ID: "empty"}},
		Scenes: []project.Scene{
			{ID: `verse<&"`, Bindings: map[string]string{`bass<&"`: `riff<&"`, "drums": "off"}},
			{ID: "chorus", Bindings: map[string]string{`bass<&"`: "keep", "drums": "beat"}},
		},
	}
	want, err := os.ReadFile("../testdata/scene-launch-matrix.html")
	if err != nil {
		t.Fatal(err)
	}
	got := gosx.RenderHTML(SceneLaunchMatrix(p, `rev<&"`, `csrf<&"`))
	if got != string(want) {
		t.Fatalf("scene launch matrix differs from the original session HTML\ngot:  %s\nwant: %s", got, want)
	}
}
