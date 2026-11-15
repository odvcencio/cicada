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
	"sync/atomic"
	"testing"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

// The rule these tests check: Studio closes, hides or refuses a Code recovery
// copy only when its text is, at that moment, exactly the text of the file.
// Text that differs from the file is kept, whatever was saved or discarded
// earlier.

// recoveryDisk is a service whose file text changes when Studio saves it and
// when a test moves it (as Undo, another client or an outside editor would).
// name is the project file's name; "" means a project that lists no files.
// The hooks act on GET /api/files/revision, the read a checkpoint makes.
type recoveryDisk struct {
	mu                   sync.Mutex
	name, text, revision string
	saves, revisionReads int
	revisionDelay        time.Duration
	revisionStatus       int
	noRevisionRoute      bool                // a service from before the route
	beforeRevisionRead   func(n int)         // runs before read n is answered
	afterRevisionRead    func()              // runs once the answer is sent
	rewriteSave          func(string) string // the service writes other text
}

func (d *recoveryDisk) set(text string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.text, d.revision = text, edits.Revision([]byte(text))
}

func (d *recoveryDisk) get() (string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.text, d.revision
}

func (d *recoveryDisk) reads() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.revisionReads
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
		case r.URL.Path == "/api/files/revision":
			disk.mu.Lock()
			disk.revisionReads++
			n, before := disk.revisionReads, disk.beforeRevisionRead
			disk.mu.Unlock()
			if before != nil {
				before(n)
			}
			disk.mu.Lock()
			delay, status, after, missing := disk.revisionDelay, disk.revisionStatus, disk.afterRevisionRead, disk.noRevisionRoute
			disk.afterRevisionRead = nil
			disk.mu.Unlock()
			if missing {
				http.NotFound(w, r)
				return
			}
			time.Sleep(delay)
			text, revision := disk.get()
			if status != 0 || r.URL.Query().Get("file") != disk.name {
				if status == 0 {
					status = http.StatusUnprocessableEntity
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "unavailable"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"file": disk.name, "revision": revision, "textRevision": edits.Revision([]byte(strings.ReplaceAll(text, "\r\n", "\n")))})
			if after != nil {
				w.(http.Flusher).Flush()
				after()
			}
		case (r.URL.Path == "/api/source" || r.URL.Path == "/api/files") && r.Method == http.MethodPost:
			var body struct {
				Source string `json:"source"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			written := body.Source
			if disk.rewriteSave != nil {
				written = disk.rewriteSave(written)
			}
			disk.set(written)
			disk.mu.Lock()
			disk.saves++
			disk.mu.Unlock()
			_, revision := disk.get()
			// As the service does, the answer carries the text it wrote.
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": revision, "valid": true, "source": written})
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
	status   int
	message  string
	kept     *bool
	revision string
	took     time.Duration
}

// checkpoint sends a recovery copy based on the file's current revision.
func (h *recoveryHarness) checkpoint(id string, sequence int, text string) checkpointReply {
	h.t.Helper()
	_, revision := h.disk.get()
	return h.checkpointAt(id, sequence, revision, text)
}

func (h *recoveryHarness) checkpointAt(id string, sequence int, revision, text string) checkpointReply {
	h.t.Helper()
	return h.checkpointWith(id, sequence, revision, text, nil)
}

func (h *recoveryHarness) checkpointWith(id string, sequence int, revision, text string, extra map[string]string) checkpointReply {
	h.t.Helper()
	fields := map[string]string{"recoveryId": strings.Repeat(id, 32), "recoverySequence": fmt.Sprint(sequence), "fileRevision": revision, "file": h.disk.name, "content": text}
	for name, value := range extra {
		fields[name] = value
	}
	start := time.Now()
	r := noteJSONPost(h.t, h.client, h.url, "/__actions/source-checkpoint", h.csrf, fields)
	took := time.Since(start)
	var body struct {
		Message string `json:"message"`
		Data    struct {
			Kept     *bool  `json:"kept"`
			Revision string `json:"revision"`
		} `json:"data"`
	}
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	h.t.Logf("checkpoint %s seq %d: %d in %v %s", id, sequence, r.StatusCode, took.Round(time.Millisecond), strings.TrimSpace(string(data)))
	return checkpointReply{r.StatusCode, body.Message, body.Data.Kept, body.Data.Revision, took}
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
	return h.storedCopy(id).State
}

func (h *recoveryHarness) storedCopy(id string) savedSource {
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
	return copy
}

func (r checkpointReply) notKept() bool {
	return r.status == http.StatusOK && r.kept != nil && !*r.kept
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
	if _, revision := h.disk.get(); !late.notKept() || late.revision != revision || !strings.Contains(late.message, "The score file has this text") {
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

// A delayed checkpoint of the copy a save named (its sequence is not above
// the save's) is refused, and a refusal reads nothing from the service.
func TestARefusedCheckpointReadsNothing(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	saved := "Z\n// saved\n"
	h.checkpointAt("f", 1, base, saved)
	if code := h.action("f", map[string]string{"content": saved}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	before := h.disk.reads()
	for _, text := range []string{saved, saved + "// other\n", saved, saved, saved} {
		if reply := h.checkpointAt("f", 1, base, text); reply.status != http.StatusConflict {
			t.Fatalf("a delayed checkpoint of the saved copy was answered %+v", reply)
		}
	}
	if reads := h.disk.reads() - before; reads != 0 {
		t.Fatalf("5 refused checkpoints read the file %d times", reads)
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

// CRLF counting as LF is the only normalization, in the checkpoint, the
// render and the save alike. A lone CR is a change: the grammar keeps it
// inside strings and comments.
func TestRecoveryComparesLineBreaksOnly(t *testing.T) {
	for _, c := range []struct {
		copy, file string
		same       bool
	}{
		{"a\nb\n", "a\nb\n", true},
		{"a\nb\n", "a\r\nb\r\n", true},
		{"a\r\nb\r\n", "a\nb\n", true},
		{"a\nb\n", "a\rb\r", false},
		{"a\r\nb", "a\nb", true},
		{"// x\r y\n", "// x\n y\n", false},
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
	if reply := h.checkpoint("4", 2, "Z\n// moved\n"); !reply.wasKept() {
		t.Fatalf("a copy that differs from a CR file by its line breaks was not kept: %+v", reply)
	}
	h.disk.set("Z\r\n// moved\r\n")
	h.checkpoint("3", 1, "Z\n// moved\n// typed\n")
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// typed") || h.copyState("4") != "closed" {
		t.Fatal("the render did not close a copy equal to a CRLF file, or hid the real draft")
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

// An edit typed after Save or Discard was pressed is kept, also when its
// checkpoint lands after the request closed the copy.
func TestAnEditTypedAfterSaveOrDiscardIsKeptWhenItLandsLate(t *testing.T) {
	for _, intent := range []string{"save", "discard"} {
		h := newRecoveryHarness(t, "song.cicada", "Z\n")
		_, base := h.disk.get()
		draft := "Z\n// draft\n"
		h.checkpointAt("d", 1, base, draft)
		fields := map[string]string{"content": draft}
		if intent == "discard" {
			fields["intent"] = "discard"
		}
		if code := h.action("d", fields); code != http.StatusSeeOther {
			t.Fatalf("%s: %d", intent, code)
		}
		typed := draft + "// typed after pressing " + intent + "\n"
		if reply := h.checkpointAt("d", 2, base, typed); !reply.wasKept() {
			t.Fatalf("%s: the edit typed after it was answered %+v", intent, reply)
		}
		if v := h.view(); !v.unsaved || !strings.Contains(v.page, "typed after pressing "+intent) {
			t.Fatalf("%s: the edit typed after it has no recovery copy", intent)
		}
	}
}

// Window 1 opens window 2's copy and saves it as it is; window 2 types on.
func TestSavingAnotherWindowsCopyLeavesThatWindowAbleToType(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	text := "Z\n// window 2\n"
	h.checkpointAt("2", 3, base, text)
	if code := h.action("2", map[string]string{"content": text, "recoverySequence": "3"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if reply := h.checkpointAt("2", 4, base, text+"// window 2 types on\n"); !reply.wasKept() {
		t.Fatalf("window 2's later typing was answered %+v", reply)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "window 2 types on") {
		t.Fatal("window 2's later typing has no recovery copy")
	}
}

// Window B opens window A's copy P, edits it into a fork F and saves or
// discards F. That closes P, at P's own sequence: window A's later typing is
// kept.
func TestAForkClosesItsParentButNotTheParentsWindow(t *testing.T) {
	for _, intent := range []string{"save", "discard"} {
		h := newRecoveryHarness(t, "song.cicada", "Z\n")
		parent := "Z\n// A: verse idea\n// A: bridge idea\n"
		h.checkpoint("a", 3, parent)
		fork := "Z\n// A: verse idea\n// B: new bridge\n"
		link := map[string]string{"recoveryParentId": strings.Repeat("a", 32), "recoveryParentHash": sourceFingerprint(parent)}
		_, base := h.disk.get()
		h.checkpointWith("b", 1, base, fork, link)
		fields := map[string]string{"content": fork, "recoveryParentId": link["recoveryParentId"], "recoveryParentHash": link["recoveryParentHash"]}
		if intent == "discard" {
			fields["intent"] = "discard"
		}
		if code := h.action("b", fields); code != http.StatusSeeOther {
			t.Fatalf("%s: %d", intent, code)
		}
		if copy := h.storedCopy("a"); copy.State != "closed" || copy.Sequence != 3 {
			t.Fatalf("%s: the parent is %s at %d", intent, copy.State, copy.Sequence)
		}
		if reply := h.checkpointAt("a", 4, base, parent+"// A keeps typing\n"); !reply.wasKept() {
			t.Fatalf("%s: window A's later typing was answered %+v", intent, reply)
		}
		if v := h.view(); !v.unsaved || !strings.Contains(v.page, "A keeps typing") {
			t.Fatalf("%s: window A's later typing has no recovery copy", intent)
		}
	}
}

// The file moves to Y right after the checkpoint read it as X: the copy of
// X, closed for matching, is reopened.
func TestACopyIsReopenedWhenTheFileMovesBetweenTheReadAndTheClose(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n// X\n")
	h.disk.mu.Lock()
	h.disk.afterRevisionRead = func() { h.disk.set("Z\n// Y from another window\n") }
	h.disk.mu.Unlock()
	if reply := h.checkpoint("c", 1, "Z\n// X\n"); !reply.wasKept() {
		t.Fatalf("the copy of X was answered %+v although the file moved to Y", reply)
	}
	if state := h.copyState("c"); state != "ready" {
		t.Fatalf("the copy of X is %s", state)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// X") {
		t.Fatal("X is not offered")
	}
}

// A render closes the copies equal to the file it shows, then reads the
// file's revision again. If the file moved in between, the copies it closed
// are reopened and offered on that same page, not one page later.
func TestACopyReopenedByARenderIsOfferedOnThatPage(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	x := "Z\n// X\n"
	if reply := h.checkpointAt("c", 1, base, x); !reply.wasKept() {
		t.Fatalf("the copy of X was answered %+v", reply)
	}
	h.disk.set(x) // another window saves X; no checkpoint or render since
	var moved atomic.Bool
	h.disk.mu.Lock()
	h.disk.beforeRevisionRead = func(int) {
		// The render's read, after it closed the copy of X.
		if moved.CompareAndSwap(false, true) {
			h.disk.set("Z\n// Y from another window\n")
		}
	}
	h.disk.mu.Unlock()
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// X") {
		t.Fatal("the copy the render reopened is not offered on that page")
	}
	if !moved.Load() {
		t.Fatal("the render did not read the file's revision after closing the copy")
	}
	if state := h.copyState("c"); state != "ready" {
		t.Fatalf("the copy of X is %s", state)
	}
}

// A read that is slow, fails or does not know the file keeps the copy.
func TestASlowFailedOrMissingFileReadKeepsTheCopy(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	h.disk.mu.Lock()
	h.disk.revisionDelay = 3 * time.Second
	h.disk.mu.Unlock()
	if reply := h.checkpoint("1", 1, "Z\n"); !reply.wasKept() || reply.took > 2900*time.Millisecond {
		t.Fatalf("slow read: %+v", reply)
	}
	h.disk.mu.Lock()
	h.disk.revisionDelay, h.disk.revisionStatus = 0, http.StatusInternalServerError
	h.disk.mu.Unlock()
	if reply := h.checkpoint("2", 1, "Z\n"); !reply.wasKept() {
		t.Fatalf("failed read: %+v", reply)
	}
	h.disk.mu.Lock()
	h.disk.revisionStatus = http.StatusUnprocessableEntity
	h.disk.mu.Unlock()
	if reply := h.checkpoint("3", 1, "Z\n"); !reply.wasKept() {
		t.Fatalf("missing file: %+v", reply)
	}
}

// A copy closed because it matched the file refuses its delayed checkpoints
// and keeps its later edits.
func TestACopyClosedForMatchingTheFileRefusesStaleCheckpoints(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	h.checkpointAt("e", 1, base, "Z\n// typing\n")
	if reply := h.checkpointAt("e", 2, base, "Z\n"); !reply.notKept() {
		t.Fatalf("a checkpoint equal to the file was answered %+v", reply)
	}
	if reply := h.checkpointAt("e", 1, base, "Z\n// typing\n"); reply.status != http.StatusConflict {
		t.Fatalf("a delayed checkpoint was answered %+v", reply)
	}
	if reply := h.checkpointAt("e", 2, base, "Z\n// other text, same sequence\n"); reply.status != http.StatusConflict {
		t.Fatalf("other text at the closed sequence was answered %+v", reply)
	}
	if reply := h.checkpointAt("e", 3, base, "Z\n// new\n"); !reply.wasKept() {
		t.Fatalf("a later edit was answered %+v", reply)
	}
	if copy := h.storedCopy("e"); copy.State != "ready" || copy.Sequence != 3 {
		t.Fatalf("the later edit is %s at %d", copy.State, copy.Sequence)
	}
}

// Discard names edit 1, but edit 2 has already landed: edit 2 is kept.
func TestDiscardKeepsAnEditThatLandedBeforeIt(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	draft := "Z\n// draft\n"
	h.checkpoint("d", 1, draft)
	h.checkpoint("d", 2, draft+"// typed after Discard\n")
	if code := h.action("d", map[string]string{"intent": "discard", "content": draft}); code != http.StatusSeeOther {
		t.Fatalf("discard: %d", code)
	}
	if state := h.copyState("d"); state != "ready" {
		t.Fatalf("the newer edit is %s", state)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "typed after Discard") {
		t.Fatal("the newer edit is not offered")
	}
}

// The service can write other text than it was sent (a tempo change re-fits
// an elastic vocal). A save compares the other copies with what it wrote.
func TestASaveComparesCopiesWithTheTextTheServiceWrote(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	sent := "Z\n// tempo 120\n"
	written := sent + "// vocal re-stretched\n"
	h.checkpoint("9", 1, sent)    // another window holds the text sent
	h.checkpoint("7", 1, written) // and one holds the text written
	h.checkpoint("f", 1, sent)    // the saving window's own copy
	h.disk.mu.Lock()
	h.disk.rewriteSave = func(text string) string { return text + "// vocal re-stretched\n" }
	h.disk.mu.Unlock()
	if code := h.action("f", map[string]string{"content": sent}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if text, _ := h.disk.get(); text != written {
		t.Fatalf("the service wrote %q", text)
	}
	if state := h.copyState("9"); state != "ready" {
		t.Fatalf("a copy of the text sent, which the file does not hold, is %s", state)
	}
	if state := h.copyState("7"); state != "closed" {
		t.Fatalf("a copy of the text written is %s", state)
	}
	if reply := h.checkpointAt("f", 1, base, sent); reply.status != http.StatusConflict {
		t.Fatalf("a delayed checkpoint of the saved copy was answered %+v", reply)
	}
}

// The file moves to a copy's text outside Studio and, before any render,
// Studio saves other text: the copy never matched while Studio looked, and
// it differs from the file, so it is kept.
func TestACopyThatMatchedOnlyUnseenIsKept(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	h.checkpoint("c", 1, "Z\n// X\n")
	h.disk.set("Z\n// X\n") // Undo from another client; no render here
	if code := h.action("7", map[string]string{"content": "Z\n// W\n"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// X") {
		t.Fatal("the copy of X, which differs from the file W, is not offered")
	}
}

// A checkpoint sent before Save and delayed lands after it: at or below the
// Save's sequence it is refused, whatever its text. Typing after the save is
// kept, and typing back to the saved text is answered kept:false.
func TestADelayedCheckpointAfterASave(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	one, two := "Z\n// one\n", "Z\n// one two\n"
	h.checkpointAt("d", 1, base, one)
	h.checkpointAt("d", 2, base, two)
	if code := h.action("d", map[string]string{"content": two, "recoverySequence": "2"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if reply := h.checkpointAt("d", 1, base, one); reply.status != http.StatusConflict {
		t.Fatalf("a delayed checkpoint of older text was answered %+v", reply)
	}
	if reply := h.checkpointAt("d", 2, base, two); reply.status != http.StatusConflict {
		t.Fatalf("a delayed checkpoint of the saved text was answered %+v", reply)
	}
	if v := h.view(); v.unsaved || strings.Contains(v.page, "// one\n") {
		t.Fatal("a delayed checkpoint came back as an unsaved draft")
	}
	if reply := h.checkpointAt("d", 3, base, two+"// typed after\n"); !reply.wasKept() || h.copyState("d") != "ready" {
		t.Fatalf("typing after the save was answered %+v", reply)
	}
	if reply := h.checkpointAt("d", 4, base, two); !reply.notKept() || reply.revision != edits.Revision([]byte(two)) || h.copyState("d") != "closed" {
		t.Fatalf("typing back to the saved text was answered %+v", reply)
	}
}

// A tab reloaded on a restored copy saves it as it is: the copy closes at the
// restored sequence, so the tab that kept it can type on. A form without a
// sequence closes its copy for good.
func TestASaveAfterAReloadClosesAtTheRestoredSequence(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	restored := "Z\n// restored\n"
	h.checkpointAt("b", 7, base, restored)
	if code := h.action("b", map[string]string{"content": restored, "recoverySequence": "7"}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if copy := h.storedCopy("b"); copy.State != "closed" || copy.Sequence != 7 {
		t.Fatalf("the restored copy is %s at %d", copy.State, copy.Sequence)
	}
	if reply := h.checkpointAt("b", 8, base, restored+"// the original tab types on\n"); !reply.wasKept() {
		t.Fatalf("the original tab's typing was answered %+v", reply)
	}
	if code := h.action("c", map[string]string{"content": "Z\n// x\n", "recoverySequence": ""}); code != http.StatusSeeOther {
		t.Fatalf("save: %d", code)
	}
	if copy := h.storedCopy("c"); copy.Sequence != ^uint64(0) {
		t.Fatalf("a form without a sequence closed its copy at %d", copy.Sequence)
	}
}

// Every kind of line ending, against a copy as a browser reports it (LF).
func TestRecoveryCopiesOfFilesWithEveryKindOfLineEnding(t *testing.T) {
	for _, c := range []struct {
		name, file, copy string
		same             bool
	}{
		{"LF", "a\nb\n", "a\nb\n", true},
		{"CRLF", "a\r\nb\r\n", "a\nb\n", true},
		{"CRLF, no final newline", "a\r\nb", "a\nb", true},
		{"LF and CRLF mixed", "a\r\nb\n", "a\nb\n", true},
		{"a lone CR", "a\rb\n", "a\nb\n", false},
		{"a byte order mark", "\ufeffa\n", "\ufeffa\n", true},
		{"a missing final newline", "a\nb", "a\nb\n", false},
	} {
		h := newRecoveryHarness(t, "song.cicada", c.file)
		_, base := h.disk.get()
		h.checkpointAt("1", 1, base, c.copy+"x")
		reply := h.checkpointAt("1", 2, base, c.copy)
		if reply.notKept() != c.same || reply.wasKept() == c.same {
			t.Fatalf("%s: the checkpoint was answered %+v", c.name, reply)
		}
		if v := h.view(); v.unsaved == c.same || (h.copyState("1") == "closed") != c.same {
			t.Fatalf("%s: after a page load the copy is %s, shown unsaved %v", c.name, h.copyState("1"), v.unsaved)
		}
	}
}

// The kept:false answer names the revision the file has at the second read;
// if that read fails, the copy is kept.
func TestTheAnswerNamesTheRevisionTheFileHasNow(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	text := "Z\n// X\n"
	h.disk.set(text)
	if reply := h.checkpointAt("a", 1, base, text); !reply.notKept() || reply.revision != edits.Revision([]byte(text)) {
		t.Fatalf("equal: %+v", reply)
	}
	h.disk.mu.Lock()
	h.disk.beforeRevisionRead = func(n int) {
		if n == 4 { // the second read of the next checkpoint
			h.disk.mu.Lock()
			h.disk.revisionStatus = http.StatusInternalServerError
			h.disk.mu.Unlock()
		}
	}
	h.disk.mu.Unlock()
	if reply := h.checkpointAt("b", 1, base, text); !reply.wasKept() || h.copyState("b") != "ready" {
		t.Fatalf("a failed second read: %+v, copy %s", reply, h.copyState("b"))
	}
}

// The kept:false answer was lost (a timeout, a navigation) and the browser
// sends the same checkpoint again: it gets the same answer, or its copy back
// if the file has moved on meanwhile.
func TestALostNotKeptAnswerIsGivenAgain(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	text := "Z\n// X\n"
	h.disk.set(text)
	first := h.checkpointAt("a", 3, base, text)
	if !first.notKept() {
		t.Fatalf("first: %+v", first)
	}
	if again := h.checkpointAt("a", 3, base, text); !again.notKept() || again.revision != first.revision {
		t.Fatalf("the repeat was answered %+v", again)
	}
	h.disk.set("Z\n// the file moved on\n")
	if again := h.checkpointAt("a", 3, base, text); !again.wasKept() || h.copyState("a") != "ready" {
		t.Fatalf("the repeat after the file moved was answered %+v, copy %s", again, h.copyState("a"))
	}
	if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// X") {
		t.Fatal("the text the file no longer holds is not offered")
	}
}

// Window B saved D, so window A's checkpoint of D closes A's copy. Before the
// checkpoint reads the file again, A presses Discard and a third window saves
// E. The Discard stands: D is not offered again.
func TestADiscardDuringACheckpointIsNotUndone(t *testing.T) {
	h := newRecoveryHarness(t, "song.cicada", "Z\n")
	_, base := h.disk.get()
	draft, third := "Z\n// draft D\n", "Z\n// third window E\n"
	h.checkpointAt("d", 1, base, draft)
	h.disk.set(draft) // window B saves D
	start := h.disk.reads()
	h.disk.mu.Lock()
	h.disk.beforeRevisionRead = func(n int) {
		if n != start+2 {
			return
		}
		_, revision := h.disk.get()
		values := map[string]string{"intent": "discard", "recoveryId": strings.Repeat("d", 32), "recoverySequence": "2", "fileRevision": revision, "revision": revision, "file": h.disk.name, "content": draft}
		if code := noteJSONPost(h.t, h.client, h.url, "/__actions/source", h.csrf, values).StatusCode; code != http.StatusSeeOther {
			h.t.Errorf("discard: %d", code)
		}
		h.disk.set(third)
	}
	h.disk.mu.Unlock()
	reply := h.checkpointAt("d", 2, base, draft)
	if reply.wasKept() {
		t.Fatalf("the checkpoint said a copy was kept: %+v", reply)
	}
	if state := h.copyState("d"); state != "closed" {
		t.Fatalf("the discarded copy is %s", state)
	}
	if v := h.view(); v.unsaved || strings.Contains(v.page, "// draft D") {
		t.Fatal("the discarded draft is offered again")
	}
}

// A Studio running against a service from before the revision route (both
// are not always updated together) compares with the file's text instead.
func TestAServiceWithoutTheRevisionRouteIsReadInFull(t *testing.T) {
	for _, name := range []string{"song.cicada", ""} {
		h := newRecoveryHarness(t, name, "Z\n")
		h.disk.mu.Lock()
		h.disk.noRevisionRoute = true
		h.disk.mu.Unlock()
		saved := "Z\n// saved\n"
		h.checkpoint("e", 1, saved)
		if code := h.action("f", map[string]string{"content": saved}); code != http.StatusSeeOther {
			t.Fatalf("%q save: %d", name, code)
		}
		if state := h.copyState("e"); state != "closed" {
			t.Fatalf("%q: a copy of the saved text is %s", name, state)
		}
		if reply := h.checkpoint("a", 1, saved); !reply.notKept() || reply.revision != edits.Revision([]byte(saved)) {
			t.Fatalf("%q: a late checkpoint of the file's text was answered %+v", name, reply)
		}
		if reply := h.checkpoint("b", 1, saved+"// other\n"); !reply.wasKept() {
			t.Fatalf("%q: a copy of other text was answered %+v", name, reply)
		}
		if v := h.view(); !v.unsaved || !strings.Contains(v.page, "// other") {
			t.Fatalf("%q: the unsaved copy is not offered", name)
		}
	}
}
