package main

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func privateNoteClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestLocalStartupRestoresOwnerAfterCookieLossWithoutPromotingViewer(t *testing.T) {
	dir := t.TempDir()
	app := configuredNoteRecoveryApp(t, dir, true)
	startup := app.Config.Handler.(interface{ StartupPath() string }).StartupPath()
	viewer := privateNoteClient()
	viewerPage := getPage(t, viewer, app.URL+"/?panel=live")
	values := map[string]string{"takeId": strings.Repeat("a", 32), "sequence": "1", "revision": "recorded", "recordings": `[{"track":"keys","pattern":"phrase","notes":[{"tick":3857,"endTick":4110,"note":64,"velocity":105}]}]`}
	if r := noteJSONPost(t, viewer, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, viewerPage), values); r.StatusCode != 403 {
		t.Fatal("viewer preempted the native owner", r.StatusCode)
	}
	owner := privateNoteClient()
	r, err := owner.Get(app.URL + startup)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 303 || r.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("native startup did not redirect safely", r.StatusCode)
	}
	page := getPage(t, owner, app.URL+"/?panel=live")
	if strings.Contains(page, "/__local/") {
		t.Fatal("startup capability leaked into workspace HTML")
	}
	if r := noteJSONPost(t, owner, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, page), values); r.StatusCode != 200 {
		t.Fatal("local owner could not save notes", r.StatusCode)
	}
	r, err = owner.Get(app.URL + startup)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal("startup capability was reusable")
	}
	app.Close()
	app = configuredNoteRecoveryApp(t, dir, true)
	startup = app.Config.Handler.(interface{ StartupPath() string }).StartupPath()
	// Simulate an expired or cleared browser session, including a viewer
	// cookie present before the native startup capability is consumed.
	owner = privateNoteClient()
	before := getPage(t, owner, app.URL+"/?panel=live")
	oldCSRF := tokenFromPage(t, before)
	r, err = owner.Get(app.URL + startup)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatal("local owner could not recover after cookie loss", r.StatusCode)
	}
	page = getPage(t, owner, app.URL+"/?panel=live")
	if !strings.Contains(page, "2 notes") && !strings.Contains(page, "1 note · Last recording checkpoint") {
		t.Fatal("native identity recovery lost private note checkpoint")
	}
	if r := noteJSONPost(t, owner, app.URL, "/__actions/note-checkpoint", oldCSRF, values); r.StatusCode != 403 {
		t.Fatal("old viewer CSRF survived owner bootstrap")
	}
	values["sequence"] = "2"
	if r := noteJSONPost(t, owner, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, page), values); r.StatusCode != 200 {
		t.Fatal("recovered local owner lost edit permission", r.StatusCode)
	}
	viewerPage = getPage(t, viewer, app.URL+"/?panel=live")
	if strings.Contains(viewerPage, "1 note · Last recording checkpoint") {
		t.Fatal("native recovery exposed private take to viewer")
	}
	if r := noteJSONPost(t, viewer, app.URL, "/__actions/note-checkpoint", tokenFromPage(t, viewerPage), values); r.StatusCode != 403 {
		t.Fatal("restart promoted viewer", r.StatusCode)
	}
}

func TestLocalStartupKeepsEstablishedPrivateReview(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	owner := privateNoteClient()
	page := getPage(t, owner, app.URL+"/?panel=live")
	values := map[string]string{"takeId": strings.Repeat("b", 32), "sequence": "1", "revision": "recorded", "recordings": `[{"track":"keys","pattern":"phrase","notes":[{"tick":3857,"endTick":4110,"note":64,"velocity":105}]}]`}
	if r := noteJSONPost(t, owner, app.URL, "/__actions/note-preview", tokenFromPage(t, page), values); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	app.Close()
	app = configuredNoteRecoveryApp(t, dir, true)
	startup := app.Config.Handler.(interface{ StartupPath() string }).StartupPath()
	r, err := owner.Get(app.URL + startup)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 303 {
		t.Fatal("established profile could not initialize startup identity", r.StatusCode)
	}
	if page := getPage(t, owner, app.URL+"/?panel=live"); !strings.Contains(page, "Retained note take") || !strings.Contains(page, "E4") {
		t.Fatal("native startup cleared the established private receipt")
	}
}

func TestNativeStartupAuthenticatesBeforeOpeningShell(t *testing.T) {
	app := configuredNoteRecoveryApp(t, t.TempDir(), true)
	defer app.Close()
	startup := app.Config.Handler.(interface{ StartupPath() string }).StartupPath()
	owner := privateNoteClient()
	response, err := owner.Get(app.URL + startup + "/studio")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/studio" || response.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("native bootstrap: %d %v", response.StatusCode, response.Header)
	}
	page := getPage(t, owner, app.URL+"/studio")
	if !strings.Contains(page, `id="studio-shell"`) || strings.Contains(page, startup) {
		t.Fatal("native bootstrap did not open a clean shell URL")
	}
	for _, path := range []string{startup, startup + "/studio"} {
		r, err := privateNoteClient().Get(app.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("startup capability reused via %s: %d", path, r.StatusCode)
		}
	}
}
