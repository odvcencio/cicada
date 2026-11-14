package main

import (
	"net/url"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestSceneBlockSelectionUsesDisplayedRevisionAndTarget(t *testing.T) {
	p := &project.Project{Scenes: []project.Scene{{ID: "main"}, {ID: "alternate"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}}}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p})
	for _, tc := range []struct {
		name, index, key, revision string
		selected                   bool
	}{
		{"current", "1", "alternate@5+2", "current", true},
		{"stale revision", "1", "alternate@5+2", "previous", false},
		{"different target", "1", "main@1+4", "current", false},
		{"out of range", "2", "alternate@5+2", "current", false},
		{"missing revision", "1", "alternate@5+2", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"panel": {"session"}, "songBlock": {tc.index}, "songKey": {tc.key}, "songRevision": {tc.revision}}
			body := getPage(t, client, address+"/?"+q.Encode())
			if got := strings.Contains(body, `data-song-index="1" id="song-block-1" open data-song-selected`); got != tc.selected {
				t.Fatalf("selected = %v, want %v", got, tc.selected)
			}
			if !strings.Contains(body, `aria-label="Edit alternate · bars 5–6"`) || !strings.Contains(body, `name="target_key" value="alternate@5+2"`) {
				t.Fatal("missing edit link or guarded form")
			}
		})
	}
}
