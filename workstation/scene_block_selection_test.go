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

func TestShellSceneArrangementControls(t *testing.T) {
	p := &project.Project{
		Edition: 2, TempoMilli: 120000,
		Tracks: []project.Track{{ID: "bass", Kind: "acid"}},
		Scenes: []project.Scene{{ID: "main"}, {ID: "alternate"}},
		Song:   []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 4}},
	}
	address, client, _ := testAppWorkspace(t, &workspace{Revision: "current", SourceRevision: "sources", Valid: true, Project: p})
	body := getPage(t, client, address+"/studio?songBlock=1&songKey=alternate%405%2B4")
	start := strings.Index(body, `id="arrangement"`)
	if start < 0 {
		t.Fatal("missing arrangement")
	}
	body = body[start:]
	end := strings.Index(body, `id="view-panel-session"`)
	if end < 0 {
		t.Fatal("missing session panel after arrangement")
	}
	body = body[:end]
	last := -1
	for _, label := range []string{"Play full song", "Stop listening", "Loop an exact range", "Add section", ">Project</summary>"} {
		at := strings.Index(body, label)
		if at <= last {
			t.Fatalf("tool %q missing or out of order", label)
		}
		last = at
	}
	for _, want := range []string{
		"Scene matrix · 8 bars · 0:16 at 120.0 BPM · Click a clip to edit it below. ↳ carries the previous pattern.",
		`data-song-index="0" id="song-block-0"`,
		`data-song-index="1" id="song-block-1" open data-song-selected`,
		`aria-label="Rename bass"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `id="song-block-0" open data-song-selected`) || strings.Contains(body, "Scene launch matrix") {
		t.Error("unselected block selected or launch matrix present in Arrange")
	}
	legacy := getPage(t, client, address+"/?panel=session")
	if !strings.Contains(legacy, "Scene launch matrix") {
		t.Error("legacy session lacks scene launch matrix")
	}
}
