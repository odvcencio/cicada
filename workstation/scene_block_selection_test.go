package main

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestSceneBlockSelectionUsesDisplayedTarget(t *testing.T) {
	p := &project.Project{Scenes: []project.Scene{{ID: "main"}, {ID: "alternate"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 2}}}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p})
	for _, tc := range []struct {
		name, index, key string
		selected         bool
	}{
		{"current", "1", "alternate@5+2", true},
		{"different target", "1", "main@1+4", false},
		{"out of range", "2", "alternate@5+2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"panel": {"session"}, "songBlock": {tc.index}, "songKey": {tc.key}}
			body := getPage(t, client, address+"/?"+q.Encode())
			if got := strings.Contains(body, `data-song-index="1" id="song-block-1" open data-song-selected`); got != tc.selected {
				t.Fatalf("selected = %v, want %v", got, tc.selected)
			}
			if strings.Contains(body, "songRevision") {
				t.Fatal("rendered markup contains songRevision")
			}
			if !strings.Contains(body, `aria-label="Edit alternate · bars 5–6"`) || !strings.Contains(body, `name="target_key" value="alternate@5+2"`) {
				t.Fatal("missing edit link or guarded form")
			}
		})
	}
}

func TestSceneBlockEditLinksDropRevealAndRevision(t *testing.T) {
	p := &project.Project{Scenes: []project.Scene{{ID: "main"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}}}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p})
	body := getPage(t, client, address+"/studio?reveal=sampler&songRevision=old")
	links := regexp.MustCompile(`<a [^>]*class="timeline-edit"[^>]*>`).FindAllString(body, -1)
	if len(links) != 1 {
		t.Fatalf("got %d Edit links, want 1", len(links))
	}
	for _, link := range links {
		if strings.Contains(link, "reveal") || strings.Contains(link, "songRevision") {
			t.Errorf("Edit link retains one-shot or revision parameter: %s", link)
		}
	}
}
