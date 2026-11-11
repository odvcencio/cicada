package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestSourceCheckpointsSurviveServiceRestartAndPortChange(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=code"))
	values := map[string]string{"recoveryId": strings.Repeat("a", 32), "recoverySequence": "1", "fileRevision": "recorded", "file": "", "content": "// unfinished variation 🎹\npattern incomplete {\n"}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", "", values); r.StatusCode != 403 {
		t.Fatal("recovery accepted without CSRF", r.StatusCode)
	}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 200 {
		data, _ := io.ReadAll(r.Body)
		t.Fatal("checkpoint failed", r.StatusCode, string(data))
	}
	app.Close()
	app = noteRecoveryApp(t, dir)
	page := getPage(t, client, app.URL+"/?panel=code")
	if !strings.Contains(page, "unfinished variation 🎹") || !strings.Contains(page, "The score file is unchanged") || !strings.Contains(page, "data-code-unsaved") {
		t.Fatal("a new service or port lost the private unsaved source")
	}
	otherJar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: otherJar}
	if strings.Contains(getPage(t, other, app.URL+"/?panel=code"), "unfinished variation") {
		t.Fatal("another session read private source")
	}
	csrf = tokenFromPage(t, page)
	values["recoverySequence"], values["intent"] = "2", "clear"
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 200 {
		t.Fatal("clearing a clean buffer failed", r.StatusCode)
	}
	values["intent"], values["recoverySequence"] = "", "3"
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 409 {
		t.Fatal("a delayed write revived the cleared draft", r.StatusCode)
	}
	if strings.Contains(getPage(t, client, app.URL+"/?panel=code"), "unfinished variation") {
		t.Fatal("a cleared recovery copy returned")
	}
}

func TestFailedCodeRecoveryRetainsARejectedSubmission(t *testing.T) {
	dir := t.TempDir()
	app := noteRecoveryApp(t, dir)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=code"))
	values := map[string]string{"recoveryId": strings.Repeat("b", 32), "recoverySequence": "1", "fileRevision": "recorded", "file": "", "content": "// checkpoint\n"}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 200 {
		t.Fatal("initial checkpoint failed", r.StatusCode)
	}
	files, err := filepath.Glob(filepath.Join(dir, "sources", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	prior, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(files[0], []byte("broken recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	values["recoverySequence"], values["content"] = "2", "// keep the rejected source 🎹\n"
	response := noteJSONPost(t, client, app.URL, "/__actions/source", csrf, values)
	if response.StatusCode == 200 {
		t.Fatal("the fake backend unexpectedly saved source")
	}
	page := getPage(t, client, app.URL+"/?panel=code")
	if !strings.Contains(page, "keep the rejected source 🎹") || !strings.Contains(page, "Keep this window open") {
		t.Fatal("failed private storage dropped the submission or hid its recovery limit")
	}
	if err := os.WriteFile(files[0], prior, 0600); err != nil {
		t.Fatal(err)
	}
	other := map[string]string{"recoveryId": strings.Repeat("d", 32), "recoverySequence": "1", "fileRevision": "recorded", "content": "// other window\n"}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, other); r.StatusCode != 200 {
		t.Fatal("the repaired store did not accept another window", r.StatusCode)
	}
	other["intent"] = "discard"
	if r := noteJSONPost(t, client, app.URL, "/__actions/source", csrf, other); r.StatusCode != http.StatusSeeOther {
		t.Fatal("discard failed", r.StatusCode)
	}
	if !strings.Contains(getPage(t, client, app.URL+"/?panel=code"), "keep the rejected source 🎹") {
		t.Fatal("discarding another window erased the volatile fallback")
	}
}

func TestDiscardWithoutAReceiptKeepsOtherWindowCopies(t *testing.T) {
	app := noteRecoveryApp(t, t.TempDir())
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=code"))
	values := map[string]string{"recoveryId": strings.Repeat("c", 32), "recoverySequence": "1", "fileRevision": "recorded", "content": "// another window's idea\n"}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 200 {
		t.Fatal("checkpoint failed", r.StatusCode)
	}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source", csrf, map[string]string{"intent": "discard"}); r.StatusCode != http.StatusSeeOther {
		t.Fatal("discard failed", r.StatusCode)
	}
	if !strings.Contains(getPage(t, client, app.URL+"/?panel=code"), "another window&#39;s idea") {
		t.Fatal("an action without a recovery receipt discarded another window's copy")
	}
}

// TestRecoveryCopyOfTheSavedFileIsNotUnsaved: after a Code save, a recovery
// copy holding the saved text is not unsaved work, whether it was kept before
// the save under another id or lands after it (it was in flight, or an editor
// not yet told of the save sent it). The Code panel shows no draft, so the
// editor keeps following saves and Undo. A copy of other text is kept.
func TestRecoveryCopyOfTheSavedFileIsNotUnsaved(t *testing.T) {
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "recorded", Valid: true, Project: &project.Project{Title: "Recovery"}})
		case "/api/source":
			io.WriteString(w, `{"revision":"recorded","valid":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(audio.Close)
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newConfiguredApp(b, workspaceOptions{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=code"))
	checkpoint := func(id, content string) {
		t.Helper()
		values := map[string]string{"recoveryId": strings.Repeat(id, 32), "recoverySequence": "1", "fileRevision": "older", "file": "", "content": content}
		if r := noteJSONPost(t, client, app.URL, "/__actions/source-checkpoint", csrf, values); r.StatusCode != 200 {
			data, _ := io.ReadAll(r.Body)
			t.Fatal("checkpoint failed", r.StatusCode, string(data))
		}
	}
	unsaved := func() bool { return strings.Contains(getPage(t, client, app.URL+"/?panel=code"), "data-code-unsaved") }
	saved := "// the phrase that is saved\n"
	checkpoint("e", saved)
	if !unsaved() {
		t.Fatal("an unsaved copy was not restored")
	}
	// The save names another copy; the one with the same text goes too.
	save := map[string]string{"recoveryId": strings.Repeat("f", 32), "recoverySequence": "1", "fileRevision": "recorded", "revision": "recorded", "content": saved}
	if r := noteJSONPost(t, client, app.URL, "/__actions/source", csrf, save); r.StatusCode != http.StatusSeeOther {
		data, _ := io.ReadAll(r.Body)
		t.Fatal("save failed", r.StatusCode, string(data))
	}
	if unsaved() {
		t.Fatal("a copy of the saved text survived the save")
	}
	// A checkpoint of the saved text that lands after the save is not kept.
	checkpoint("a", saved)
	if unsaved() {
		t.Fatal("a late checkpoint of the saved text was kept as unsaved work")
	}
	// Other text is.
	checkpoint("b", saved+"// typed after the save\n")
	if !unsaved() {
		t.Fatal("a real unsaved copy was not restored")
	}
}
