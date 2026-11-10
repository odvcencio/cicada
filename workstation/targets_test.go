package main

import (
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestTargetKeysNameTheItemAtAPosition(t *testing.T) {
	p := &project.Project{
		Song:       []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}},
		Modulators: []project.Modulator{{ID: "swell", Points: []project.ContourPoint{{At: 0, Value: 0}, {At: 0.5, Value: 0.8, Shape: "smooth"}}}},
		Patterns: []project.Pattern{{ID: "take", Performance: &project.Performance{
			Notes:  []project.PerformedNote{{Tick: 0, EndTick: 240, Note: 60, Expressions: []project.PerformedExpression{{Tick: 12}}}, {Tick: 480, EndTick: 720, Note: 64}},
			Groups: []project.ControlGroup{{Name: "bend", Points: []project.GroupExpression{{Tick: 0}, {Tick: 960}}}},
		}}},
	}
	for _, c := range []struct {
		path string
		form map[string]string
		want string
	}{
		{"/api/song", map[string]string{"index": "1"}, "alternate@5+2"},
		{"/api/song", map[string]string{"index": "2"}, ""},
		{"/api/automation", map[string]string{"action": "mod-source-edit", "source": "swell", "index": "1"}, "0.5:0.8:smooth"},
		{"/api/automation", map[string]string{"action": "mod-route", "source": "swell", "index": "1"}, ""},
		{"/api/pattern", map[string]string{"action": "performed-note", "pattern": "take", "index": "1"}, "480-720-64"},
		{"/api/pattern", map[string]string{"action": "performance-binding", "pattern": "take", "note_index": "0"}, "0-240-60"},
		{"/api/pattern", map[string]string{"action": "performed-gesture", "pattern": "take", "note_index": "0", "index": "0"}, "0-240-60/12"},
		{"/api/pattern", map[string]string{"action": "performance-group", "pattern": "take", "index": "0"}, "bend"},
		{"/api/pattern", map[string]string{"action": "shared-gesture", "pattern": "take", "group_index": "0", "index": "1"}, "bend/960"},
		{"/api/pattern", map[string]string{"action": "toggle", "pattern": "take", "index": "1"}, ""},
	} {
		if got := targetKey(p, c.path, c.form); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.path, c.form, got, c.want)
		}
	}
	// A block inserted earlier gives the same position another key.
	inserted := &project.Project{Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}}}
	if songBlockKey(inserted.Song, 1) == songBlockKey(p.Song, 1) {
		t.Fatal("an inserted block kept the key of the block it displaced")
	}
}

func TestSongBlockFormsCarryTheirKey(t *testing.T) {
	page := renderSongBlocks(t)
	for _, want := range []string{`name="target_key" value="main@1+4"`, `name="target_key" value="alternate@5+2"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Count(page, `name="target_key" value="alternate@5+2"`) != strings.Count(page, `name="index" value="1"`) {
		t.Fatal("a block form at index 1 has no key")
	}
}

func TestActionsRefuseATargetKeyThatNamesAnotherItem(t *testing.T) {
	saved := workspace{Revision: "current", SourceRevision: "current-sources", Valid: true, Project: &project.Project{Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}}}}
	address, client, requests := testAppWorkspace(t, &saved)
	csrf := tokenFromPage(t, getPage(t, client, address+"/?panel=code"))
	remove := url.Values{"csrf_token": {csrf}, "revision": {"current"}, "source_revision": {"current-sources"}, "action": {"delete"}, "index": {"1"}}
	// The form showed "alternate" at index 1; another change put a copy of
	// "main" there.
	remove.Set("target_key", "alternate@5+2")
	if response := post(t, client, address+"/__actions/song", remove, true, address); response.StatusCode != 409 || len(*requests) != 0 {
		t.Fatalf("stale key: status %d, requests %+v", response.StatusCode, *requests)
	}
	remove.Set("target_key", "main@5+4")
	if response := post(t, client, address+"/__actions/song", remove, true, address); response.StatusCode >= 400 || len(*requests) != 1 {
		t.Fatalf("current key: status %d, requests %+v", response.StatusCode, *requests)
	}
	// At another revision the service refuses the write; the key is not
	// checked against a project the write would not apply to.
	remove.Set("revision", "older")
	remove.Set("target_key", "alternate@5+2")
	if response := post(t, client, address+"/__actions/song", remove, true, address); len(*requests) != 2 {
		t.Fatalf("other revision: status %d, requests %+v", response.StatusCode, *requests)
	}
}

// renderSongBlocks renders the Session panel's song blocks for main (4 bars)
// then alternate (2 bars).
func renderSongBlocks(t *testing.T) string {
	t.Helper()
	p := &project.Project{
		Scenes: []project.Scene{{ID: "main"}, {ID: "alternate"}},
		Song:   []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}},
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "current-sources", Valid: true, Project: p})
	return getPage(t, client, address+"/?panel=session")
}
