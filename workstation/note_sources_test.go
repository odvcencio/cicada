package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx"
)

func TestSavedNoteProjectVersionIsImmutableAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	store, _, err := openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := savedNoteFixture()
	saved.SourceRevision = "captured-project"
	written, err := store.save("player", saved)
	if err != nil || written.Version != 4 {
		t.Fatal("source-aware checkpoint did not use version 4", written, err)
	}
	changed := saved
	changed.Sequence++
	changed.SourceRevision = "another-project"
	if _, err := store.save("player", changed); err != errNoteCheckpoint {
		t.Fatal("checkpoint changed its capture project", err)
	}
	store, _, err = openNoteStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.get("player", saved.ID)
	if err != nil || !reflect.DeepEqual(restored, written) {
		t.Fatal("restart changed the capture version or notes", err)
	}
	if noteDraft(restored, "player").SourceRevision != saved.SourceRevision {
		t.Fatal("review dropped captured sources")
	}
}

func TestProjectVersionCheckpointRoundTripsAcrossServicePorts(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	fixture := savedNoteFixture()
	takes, _ := json.Marshal(fixture.Takes)
	values := map[string]string{"takeId": fixture.ID, "sequence": "1", "revision": "recorded", "source_revision": "capture-project", "recordings": string(takes)}
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-checkpoint", csrf, values); r.StatusCode != 200 {
		t.Fatal("checkpoint failed", r.StatusCode)
	}
	values["sequence"] = "2"
	if r := noteJSONPost(t, client, app.URL, "/__actions/note-preview", csrf, values); r.StatusCode != 200 {
		t.Fatal("review failed", r.StatusCode)
	}
	app.Close()
	app = noteRecoveryApp(t, dir)
	r, err := client.Get(app.URL + "/api/note-recovery/" + fixture.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var restored savedNotes
	if err := json.NewDecoder(r.Body).Decode(&restored); err != nil || r.StatusCode != 200 || restored.Version != 4 || restored.SourceRevision != "capture-project" || !reflect.DeepEqual(restored.Takes, fixture.Takes) {
		t.Fatal("server restart lost the source receipt or captured notes", r.StatusCode, err)
	}
}

func TestNoteReviewChecksAreScopedToTheWholeProject(t *testing.T) {
	takes, _ := json.Marshal([]control.Recording{{Track: "keys", Pattern: "phrase", Notes: []control.Note{{Note: 60, Velocity: 96}}}})
	v := workspace{Revision: "same-entry", SourceRevision: "current-project", Project: &project.Project{Patterns: []project.Pattern{{ID: "phrase", Steps: 16}}}}
	d := draft{Source: string(takes), Revision: v.Revision, SourceRevision: "captured-project", CheckRevision: v.Revision, CheckSourceRevision: "captured-project", CheckError: "old grid error", PlayedCheckRevision: v.Revision, PlayedCheckSourceRevision: "captured-project", PlayedCheckError: "old played error"}
	html := gosx.RenderHTML((&studioApp{}).noteTakeReview(v, "csrf", "take", d))
	for _, want := range []string{"Project files changed since this take", `name="source_revision" value="current-project"`, `value="rebase"`, `value="played">Keep played timing`} {
		if !strings.Contains(html, want) {
			t.Fatal("source change missing from review", want)
		}
	}
	if strings.Contains(html, "old grid error") || strings.Contains(html, "old played error") || strings.Index(html, "Project files changed") > strings.Index(html, `value="played">`) {
		t.Fatal("stale check blocked or hid the current project choice")
	}
	d.SourceRevision = ""
	html = gosx.RenderHTML((&studioApp{}).noteTakeReview(v, "csrf", "take", d))
	if !strings.Contains(html, "This take has no project version") {
		t.Fatal("legacy take silently acquired a capture receipt")
	}
}

func TestBackendPreviewForwardsAnExplicitProjectReceipt(t *testing.T) {
	var got []string
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Cicada-Source-Revision"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"valid":true}`))
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{context.Background(), context.WithValue(context.Background(), expectedProjectSourcesKey{}, "displayed-project")} {
		var result struct{ Valid bool }
		if err := b.call(ctx, http.MethodPost, "/api/intents", map[string]bool{"dryrun": true}, &result); err != nil || !result.Valid {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(got, []string{"", "displayed-project"}) {
		t.Fatal("preview source receipt lost or invented", got)
	}
}
