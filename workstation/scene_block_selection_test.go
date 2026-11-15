package main

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func assertSongBlock(t *testing.T, body string, index int, selected bool) {
	t.Helper()
	attr := func(tag, name string) bool {
		return regexp.MustCompile(`\s` + regexp.QuoteMeta(name) + `(?:\s|=|/?>)`).MatchString(tag)
	}
	for _, tag := range regexp.MustCompile(`<details\b[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(tag, `id="song-block-`+strconv.Itoa(index)+`"`) {
			continue
		}
		if !strings.Contains(tag, `data-song-index="`+strconv.Itoa(index)+`"`) {
			t.Errorf("block %d missing its song index: %s", index, tag)
		}
		for _, name := range []string{"open", "data-song-selected"} {
			if got := attr(tag, name); got != selected {
				t.Errorf("block %d %s = %v, want %v: %s", index, name, got, selected, tag)
			}
		}
		return
	}
	t.Errorf("missing song block %d", index)
}

func TestSceneBlockGenerationChangesWithSong(t *testing.T) {
	p := &project.Project{Scenes: []project.Scene{{ID: "main"}}, Song: []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "main", Bars: 2}}}
	view := &workspace{Revision: "current", Filename: "main.cicada", Valid: true, Project: p}
	address, client, _ := testAppWorkspace(t, view)
	generation := func() string {
		t.Helper()
		body := getPage(t, client, address+"/?panel=session")
		tags := regexp.MustCompile(`<details\b[^>]*class="arrangement-block"[^>]*>`).FindAllString(body, -1)
		if len(tags) != 2 {
			t.Fatalf("got %d blocks, want 2", len(tags))
		}
		var result string
		for _, tag := range tags {
			match := regexp.MustCompile(`data-song-generation="([0-9a-f]{12})"`).FindStringSubmatch(tag)
			if len(match) != 2 {
				t.Fatalf("missing generation: %s", tag)
			}
			if result != "" && result != match[1] {
				t.Fatalf("block generations differ")
			}
			result = match[1]
		}
		return result
	}
	before := generation()
	p.Song[1].Bars++
	if after := generation(); after == before {
		t.Fatal("generation did not change with block length")
	}
}

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
			assertSongBlock(t, body, 0, false)
			assertSongBlock(t, body, 1, tc.selected)
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
		Song:   []project.SongEntry{{Scene: "main", Bars: 4}, {Scene: "alternate", Bars: 4}, {Scene: "main", Bars: 4}},
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
		"Scene matrix · 12 bars · 0:24 at 120.0 BPM · Click a clip to edit it below. ↳ carries the previous pattern.",
		`aria-label="Rename bass"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, tag := range regexp.MustCompile(`<details\b[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(tag, `name="arrange-tools"`) || strings.Contains(tag, `class="rail-rename"`) {
			if !strings.Contains(tag, "data-discard-on-close") {
				t.Errorf("missing discard attribute: %s", tag)
			}
			// Floating tool panels close on an outside press; the in-flow rename form does not.
			if floats := strings.Contains(tag, `name="arrange-tools"`); floats != strings.Contains(tag, "data-dismiss-outside") {
				t.Errorf("outside-press dismissal is wrong on: %s", tag)
			}
		}
	}
	if got := strings.Count(body, "data-discard-on-close"); got != 4 {
		t.Errorf("got %d discardable panels, want 4", got)
	}
	assertSongBlock(t, body, 0, false)
	assertSongBlock(t, body, 1, true)
	assertSongBlock(t, body, 2, false)
	if strings.Count(body, "data-song-selected") != 1 {
		t.Error("expected exactly one selected block")
	}
	selectedStart := strings.Index(body, `id="song-block-1"`)
	if selectedStart < 0 {
		t.Fatal("missing selected block")
	}
	selectedEnd := strings.Index(body[selectedStart:], "</details>")
	if selectedEnd < 0 {
		t.Fatal("missing end of selected block")
	}
	selected := body[selectedStart : selectedStart+selectedEnd]
	for _, label := range []string{"Loop block", "Replace scene", "Set length", "Move earlier", "Move later", "Duplicate block", "Remove block"} {
		if !strings.Contains(selected, label) {
			t.Errorf("selected block missing %q", label)
		}
	}
	if strings.Contains(body, "Scene launch matrix") {
		t.Error("launch matrix present in Arrange")
	}
	legacy := getPage(t, client, address+"/?panel=session")
	defaultShell := getPage(t, client, address+"/studio")
	for index := range p.Song {
		assertSongBlock(t, legacy, index, false)
		assertSongBlock(t, defaultShell, index, false)
	}
	if !strings.Contains(legacy, "Scene launch matrix") {
		t.Error("legacy session lacks scene launch matrix")
	}
}
