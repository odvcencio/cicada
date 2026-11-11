package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/project"
)

// The rule these tests check: Studio closes, hides or refuses a Code recovery
// copy only when its text is, at that moment, exactly the text of the file.
// Text that differs from the file is kept, whatever was saved or discarded
// earlier.

// recoveryDisk is a service whose file text changes when Studio saves it and
// when a test moves it (as Undo, another client or an outside editor would).
// name is the project file's name; "" means a project that lists no files.
type recoveryDisk struct {
	mu                   sync.Mutex
	name, text, revision string
	changes, saves       int
}

func (d *recoveryDisk) set(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.changes++
	d.text, d.revision = text, fmt.Sprintf("r%d", d.changes)
}

func (d *recoveryDisk) get() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.text, d.revision
}

type recoveryHarness struct {
	t      *testing.T
	disk   *recoveryDisk
	client *http.Client
	url    string
	csrf   string
	dir    string
}

func newRecoveryHarness(t *testing.T, name, text string) *recoveryHarness {
	t.Helper()
	disk := &recoveryDisk{name: name}
	disk.set(text)
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		text, revision := disk.get()
		var files []projectFile
		if disk.name != "" {
			files = []projectFile{{Name: disk.name, Source: text, Revision: revision}}
		}
		switch {
		case r.URL.Path == "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: revision, Valid: true, Source: text, Files: files, Project: &project.Project{Title: "Recovery"}})
		case r.URL.Path == "/api/files" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
		case (r.URL.Path == "/api/source" || r.URL.Path == "/api/files") && r.Method == http.MethodPost:
			var body struct {
				Source string `json:"source"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			disk.set(body.Source)
			disk.mu.Lock()
			disk.saves++
			disk.mu.Unlock()
			_, revision := disk.get()
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": revision, "valid": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(audio.Close)
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	handler, err := newConfiguredApp(b, workspaceOptions{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	t.Cleanup(app.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	h := &recoveryHarness{t: t, disk: disk, client: client, url: app.URL, dir: dir}
	h.csrf = tokenFromPage(t, getPage(t, client, app.URL+"/?panel=code"))
	return h
}

type checkpointReply struct {
	status  int
	message string
	kept    *bool
}

// checkpoint sends a recovery copy based on the file's current revision.
func (h *recoveryHarness) checkpoint(id string, sequence int, text string) checkpointReply {
	h.t.Helper()
	_, revision := h.disk.get()
	return h.checkpointAt(id, sequence, revision, text)
}

func (h *recoveryHarness) checkpointAt(id string, sequence int, revision, text string) checkpointReply {
	h.t.Helper()
	r := noteJSONPost(h.t, h.client, h.url, "/__actions/source-checkpoint", h.csrf, map[string]string{"recoveryId": strings.Repeat(id, 32), "recoverySequence": fmt.Sprint(sequence), "fileRevision": revision, "file": h.disk.name, "content": text})
	var body struct {
		Message string `json:"message"`
		Data    struct {
			Kept *bool `json:"kept"`
		} `json:"data"`
	}
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	h.t.Logf("checkpoint %s seq %d: %d %s", id, sequence, r.StatusCode, strings.TrimSpace(string(data)))
	return checkpointReply{r.StatusCode, body.Message, body.Data.Kept}
}

// action posts the Code form: a save, or a Discard with intent "discard".
func (h *recoveryHarness) action(id string, fields map[string]string) int {
	h.t.Helper()
	_, revision := h.disk.get()
	values := map[string]string{"recoveryId": strings.Repeat(id, 32), "recoverySequence": "1", "fileRevision": revision, "revision": revision, "file": h.disk.name}
	for name, value := range fields {
		values[name] = value
	}
	r := noteJSONPost(h.t, h.client, h.url, "/__actions/source", h.csrf, values)
	if r.StatusCode != http.StatusSeeOther {
		data, _ := io.ReadAll(r.Body)
		h.t.Logf("action: %d %s", r.StatusCode, data)
	}
	return r.StatusCode
}

type recoveryView struct {
	unsaved, otherCopies bool
	page                 string
}

func (h *recoveryHarness) view() recoveryView {
	h.t.Helper()
	page := getPage(h.t, h.client, h.url+"/?panel=code")
	return recoveryView{strings.Contains(page, "data-code-unsaved"), strings.Contains(page, "Other recovery copies"), page}
}

// copyState reads a copy's state from its file in the recovery store.
func (h *recoveryHarness) copyState(id string) string {
	h.t.Helper()
	matches, err := filepath.Glob(filepath.Join(h.dir, "sources", "*"+strings.Repeat(id, 32)+".json"))
	if err != nil || len(matches) != 1 {
		h.t.Fatal("copy file not found", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		h.t.Fatal(err)
	}
	var copy savedSource
	if err := json.Unmarshal(data, &copy); err != nil {
		h.t.Fatal(err)
	}
	return copy.State
}

func (r checkpointReply) wasKept() bool {
	return r.status == http.StatusOK && (r.kept == nil || *r.kept)
}

func TestRetypedDiscardedTextKeepsARecoveryCopy(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	discarded := "Z\n// idea B\n"
	h.checkpoint("1", 1, discarded)
	if code := h.action("1", map[string]string{"intent": "discard", "content": discarded}); code != http.StatusSeeOther {
		t.Fatalf("discard: %d", code)
	}
	if text, _ := h.disk.get(); text != "Z\n" || h.disk.saves != 0 {
		t.Fatalf("Discard wrote the file: %q", text)
	}
	// The user types or pastes the same text again; the file still holds Z.
	if reply := h.checkpoint("2", 1, discarded); !reply.wasKept() {
		t.Fatalf("the checkpoint was not kept: %+v", reply)
	}
	if !h.view().unsaved {
		t.Fatal("text equal to a discarded draft has no recovery copy, although the file holds other text")
	}
}

func TestTextSavedThenUndoneKeepsARecoveryCopy(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	saved := "Z\n// saved A\n"
	if code := h.action("1", map[string]string{"content": saved}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	h.disk.set("Z\n") // Undo, another client, or an outside editor
	if reply := h.checkpoint("2", 1, saved); !reply.wasKept() {
		t.Fatalf("the checkpoint was not kept: %+v", reply)
	}
	if !h.view().unsaved {
		t.Fatal("text that was saved once, typed again after Undo, has no recovery copy")
	}
}

func TestACopyThatMatchesTheFileIsClosedNotOnlyHidden(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	old := "Z\n// old A\n"
	h.checkpoint("c", 1, old)
	h.disk.set(old) // the file becomes A without a save that names copy c
	if h.view().unsaved {
		t.Fatal("a copy equal to the file was shown as unsaved")
	}
	if state := h.copyState("c"); state != "closed" {
		t.Fatalf("the render left a copy equal to the file %s", state)
	}
	if code := h.action("d", map[string]string{"content": "Z\n// newer D\n"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if v := h.view(); v.unsaved || strings.Contains(v.page, "// old A") {
		t.Fatal("an older copy came back as an unsaved draft over the newer file")
	}
}

func TestACopyThatMatchesTheFileDoesNotHideAnOlderDraft(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	h.checkpoint("b", 1, "Z\n// real unsaved B\n")
	h.checkpoint("f", 2, "Z\n") // newer, and equal to the file
	v := h.view()
	if !v.unsaved || !strings.Contains(v.page, "// real unsaved B") {
		t.Fatalf("the unsaved draft B was not restored; other copies listed: %v", v.otherCopies)
	}
}

func TestASaveLeavesAnotherWindowAbleToKeepTyping(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	same := "Z\n// same A\n"
	_, base := h.disk.get()
	h.checkpoint("9", 1, same) // window 2
	if code := h.action("1", map[string]string{"content": same}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	reply := h.checkpointAt("9", 2, base, same+"// window 2 keeps typing\n")
	if !reply.wasKept() {
		t.Fatalf("window 2's next checkpoint was not kept: %+v", reply)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "window 2 keeps typing") {
		t.Fatal("window 2's further typing has no recovery copy")
	}
}

func TestAnUnreadableRecoveryCopyDoesNotFailASave(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	h.checkpoint("1", 1, "Z\n// other\n")
	matches, err := filepath.Glob(filepath.Join(h.dir, "sources", "*"+strings.Repeat("1", 32)+".json"))
	if err != nil || len(matches) != 1 {
		t.Fatal("copy file not found", matches, err)
	}
	broken := strings.Replace(matches[0], strings.Repeat("1", 32), strings.Repeat("2", 32), 1)
	if err := os.WriteFile(broken, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := h.action("3", map[string]string{"content": "Z\n// saved\n"}); code != http.StatusSeeOther {
		t.Fatalf("a save that wrote the file reported %d because another copy is unreadable", code)
	}
	if text, _ := h.disk.get(); text != "Z\n// saved\n" {
		t.Fatalf("the save did not write: %q", text)
	}
	if data, err := os.ReadFile(broken); err != nil || string(data) != "{not json" {
		t.Fatal("the unreadable copy was not retained", err)
	}
}

// A copy of the text a save writes is closed: one kept before the save under
// an id the save does not name, and one that lands after it. Neither shows as
// unsaved, and the late one is answered as not kept. Other text is kept.
func TestACopyOfTheSavedTextIsNotUnsavedWork(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	saved := "Z\n// the phrase that is saved\n"
	if reply := h.checkpoint("e", 1, saved); !reply.wasKept() {
		t.Fatalf("an unsaved copy was not kept: %+v", reply)
	}
	if !h.view().unsaved {
		t.Fatal("an unsaved copy was not restored")
	}
	if code := h.action("f", map[string]string{"content": saved}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if text, _ := h.disk.get(); text != saved {
		t.Fatalf("the save did not write: %q", text)
	}
	if h.view().unsaved {
		t.Fatal("a copy of the saved text, under another id, survived the save")
	}
	late := h.checkpoint("a", 1, saved)
	if late.status != http.StatusOK || late.kept == nil || *late.kept || !strings.Contains(late.message, "The score file has this text") {
		t.Fatalf("a late checkpoint of the file's text was answered %+v", late)
	}
	if h.view().unsaved {
		t.Fatal("a late checkpoint of the file's text was kept as unsaved work")
	}
	// The same window types on: its copy is kept again.
	if reply := h.checkpoint("a", 2, saved+"// typed after the save\n"); !reply.wasKept() || !h.view().unsaved {
		t.Fatalf("typing after the save has no recovery copy: %+v", reply)
	}
}

// The save closes the copy it names for good. A delayed checkpoint of that
// copy is refused, unless the file has its text: then nothing needs keeping
// and the answer says so.
func TestALateCheckpointOfASavedCopy(t *testing.T) {
	h := newRecoveryHarness(t, "", "Z\n")
	_, base := h.disk.get()
	saved := "Z\n// saved\n"
	if code := h.action("f", map[string]string{"content": saved}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if reply := h.checkpointAt("f", 1, base, saved); reply.status != http.StatusOK || reply.kept == nil || *reply.kept {
		t.Fatalf("a delayed checkpoint of the saved text was answered %+v", reply)
	}
	if reply := h.checkpointAt("f", 1, base, saved+"// other\n"); reply.status != http.StatusConflict {
		t.Fatalf("a delayed checkpoint of other text revived a saved copy: %+v", reply)
	}
}

// A named project file is compared with that file's text.
func TestACheckpointIsComparedWithItsOwnFile(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	if reply := h.checkpoint("1", 1, "Z\n"); reply.status != http.StatusOK || reply.kept == nil || *reply.kept {
		t.Fatalf("a copy of the file's own text was answered %+v", reply)
	}
	if reply := h.checkpoint("2", 1, "Z\n// typed\n"); !reply.wasKept() {
		t.Fatalf("a copy of other text was not kept: %+v", reply)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// typed") {
		t.Fatal("the unsaved copy of the named file was not restored")
	}
}

// Line breaks are the only normalization, in the checkpoint, the render and
// the save alike: a browser reports CRLF and a lone CR as LF.
func TestRecoveryComparesLineBreaksOnly(t *testing.T) {
	for _, c := range []struct {
		copy, file string
		same       bool
	}{
		{"a\nb\n", "a\nb\n", true},
		{"a\nb\n", "a\r\nb\r\n", true},
		{"a\r\nb\r\n", "a\nb\n", true},
		{"a\nb\n", "a\rb\r", true},
		{"a\nb", "a\nb\n", false},
		{"a\nb\n", "a\nb", false},
		{"a\nb\n", "a\n\nb\n", false},
		{"a \nb\n", "a\nb\n", false},
	} {
		if got := sameSourceText(c.copy, c.file); got != c.same {
			t.Errorf("sameSourceText(%q, %q) = %v, want %v", c.copy, c.file, got, c.same)
		}
	}
	h := newRecoveryHarness(t, "", "Z\r\n// saved\r\n")
	if reply := h.checkpoint("1", 1, "Z\n// saved\n"); reply.kept == nil || *reply.kept {
		t.Fatalf("a copy of a CRLF file's text was answered %+v", reply)
	}
	if reply := h.checkpoint("2", 1, "Z\n// saved"); !reply.wasKept() {
		t.Fatalf("a copy without the final newline was not kept: %+v", reply)
	}
	h.disk.set("Z\r// moved\r")
	h.checkpoint("3", 1, "Z\n// moved\n// typed\n")
	h.checkpoint("4", 2, "Z\n// moved\n")
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// typed") {
		t.Fatal("the render did not skip a copy equal to a CR file, or hid the real draft")
	}
	if code := h.action("5", map[string]string{"content": "Z\r\n// moved\r\n// typed\r\n"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	// Checked before any render, which would close it too.
	if state := h.copyState("3"); state != "closed" {
		t.Fatalf("a save sent with CRLF left its LF copy %s", state)
	}
	if state := h.copyState("2"); state != "ready" {
		t.Fatalf("the copy without the final newline is %s", state)
	}
}
