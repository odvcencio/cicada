package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
)

// activeVocalCapture leaves one vocal take open without audio hardware.
func activeVocalCapture(t *testing.T, s *studio) string {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.takes.Begin("vox", "main", studioRevision(source), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := capture.NewRecorder(4, 4, 1, s.takes.Writer(id))
	if err != nil {
		t.Fatal(err)
	}
	count, _ := capture.NewCountIn(48000, 120000, 0)
	if err = r.Begin(count, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	r.Capture(capture.Block{DeviceEpoch: 1, EngineEpoch: 1, SampleRate: 48000, Frames: 4, Layout: capture.LayoutMono}, [][]float32{{0.25, 0.5, 1, 2}})
	s.mu.Lock()
	s.captureID, s.captureRecorder = id, r
	s.mu.Unlock()
	return id
}

type studioStateSnapshot struct {
	source  []byte
	history int
	log     int
}

func snapshotStudioState(t *testing.T, s *studio) studioStateSnapshot {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := editlog.ReadLog(editlog.Path(s.path))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return studioStateSnapshot{source: source, history: len(s.history.edits), log: len(records)}
}

// A mixer edit during a vocal take would change the revision the take publishes
// against, so every route and /api/intents must refuse it the same way.
func TestMixerEditsRefusedDuringCaptureOnEveryRoute(t *testing.T) {
	const level = `{"kind":"setparam","entity":"param:vox.level","value":-12}`
	toggle := `{"kind":"togglestep","entity":"step:pulse/1","shared":"definition"}`
	cases := []struct {
		name, route, body string
		status            int // 0 means 409
	}{
		// The vocal-chain route has always answered 422 for its own guard; it
		// is listed so the refusal and the unchanged state stay under test.
		{"vocal chain route", "/api/mixer", `{"revision":%q,"action":"vocal-chain","track":"vox"}`, http.StatusUnprocessableEntity},
		{"mixer route", "/api/mixer", `{"revision":%q,"path":"vox.level","value":-12}`, 0},
		{"intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + level + `]}`, 0},
		{"batch after another intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + toggle + `,` + level + `]}`, 0},
		{"batch before another intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + level + `,` + toggle + `]}`, 0},
		{"dry run", "/api/intents", `{"version":1,"revision":%q,"dryrun":true,"intents":[` + level + `]}`, 0},
		{"add effect", "/api/intents", `{"version":1,"revision":%q,"intents":[{"kind":"addeffect","name":"room","effectkind":"reverb"}]}`, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newTakeStudio(t, t.TempDir())
			id := activeVocalCapture(t, s)
			before := snapshotStudioState(t, s)
			revision := studioRevision(before.source)
			response := studioCallRaw(t, s.routes(), c.route, fmt.Sprintf(c.body, revision))
			want := c.status
			if want == 0 {
				want = http.StatusConflict
			}
			if response.Code != want {
				t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body)
			}
			after := snapshotStudioState(t, s)
			if !bytes.Equal(after.source, before.source) || after.history != before.history || after.log != before.log {
				t.Fatalf("refused edit left a trace: history %d->%d log %d->%d", before.history, after.history, before.log, after.log)
			}
			s.mu.Lock()
			err := s.finishVocalCapture(id)
			s.mu.Unlock()
			if err != nil {
				t.Fatalf("take did not publish after refusal: %v", err)
			}
			if take, err := s.takes.Get(id); err != nil || take.Stage != takejournal.Committed {
				t.Fatalf("take %+v %v", take, err)
			}
		})
	}
}

func studioCallRaw(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Host = "127.0.0.1:1234"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// Proposals reach the same writers, so staging and acceptance share the guard.
func TestMixerProposalsRefusedDuringCapture(t *testing.T) {
	level := `{"version":1,"revision":%q,"intents":[{"kind":"setparam","entity":"param:vox.level","value":-12}]}`
	t.Run("stage during capture", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := newTakeStudio(t, t.TempDir())
		activeVocalCapture(t, s)
		before := snapshotStudioState(t, s)
		response := studioCallRaw(t, s.routes(), "/api/proposals", fmt.Sprintf(`{"envelope":`+level+`}`, studioRevision(before.source)))
		if response.Code != http.StatusConflict {
			t.Fatalf("stage: %d %s", response.Code, response.Body)
		}
	})
	t.Run("accept during capture", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := newTakeStudio(t, t.TempDir())
		before := snapshotStudioState(t, s)
		response := studioCallRaw(t, s.routes(), "/api/proposals", fmt.Sprintf(`{"envelope":`+level+`}`, studioRevision(before.source)))
		if response.Code != http.StatusOK {
			t.Fatalf("stage: %d %s", response.Code, response.Body)
		}
		var staged struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &staged); err != nil || staged.ID == "" {
			t.Fatalf("proposal id: %v %s", err, response.Body)
		}
		id := activeVocalCapture(t, s)
		response = studioCallRaw(t, s.routes(), "/api/proposals/"+staged.ID+"/accept", "{}")
		if response.Code != http.StatusConflict {
			t.Fatalf("accept: %d %s", response.Code, response.Body)
		}
		after := snapshotStudioState(t, s)
		if !bytes.Equal(after.source, before.source) || after.history != before.history || after.log != before.log {
			t.Fatal("refused acceptance left a trace")
		}
		s.mu.Lock()
		err := s.finishVocalCapture(id)
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("take did not publish: %v", err)
		}
	})
}

// Guard parity: each refusal a single-purpose route enforces before or around
// the writer must also stop the same edit sent as an intent or applied from
// the command line. Every case leaves source, history and edit log unchanged.
func TestRouteGuardsMatchIntentsAndApply(t *testing.T) {
	edition1 := studioScore
	cases := []struct {
		name   string
		score  string
		route  string
		body   string // route request; %q is the revision
		intent string // equivalent intent object
		stray  bool   // leave an unresolved recovery file beside the score
		status int    // route status; /api/intents must refuse too, with its own status
	}{
		{"edition 1 mixer needs confirmation", edition1, "/api/mixer", `{"revision":%q,"path":"bass.level","value":-6}`,
			`{"kind":"setparam","entity":"param:bass.level","value":-6}`, false, http.StatusConflict},
		{"edition 1 mixer effect needs confirmation", edition1, "", "",
			`{"kind":"addeffect","name":"room","effectkind":"reverb"}`, false, 0},
		{"edition 1 refuses audio tracks", edition1, "/api/clip", `{"revision":%q,"action":"audio-track","newName":"vox"}`,
			`{"kind":"addaudiotrack","name":"vox"}`, false, http.StatusUnprocessableEntity},
		{"recovery file blocks mixer edit", "cicada 2\n" + studioScore, "/api/mixer", `{"revision":%q,"path":"bass.level","value":-6}`,
			`{"kind":"setparam","entity":"param:bass.level","value":-6}`, true, http.StatusConflict},
		{"recovery file blocks song conversion", "cicada 2\n" + studioScore, "", "",
			`{"kind":"convertsongtoarrange"}`, true, 0},
		{"recovery file blocks pattern edit", "cicada 2\n" + studioScore, "/api/pattern", `{"revision":%q,"action":"toggle","pattern":"pulse","step":1}`,
			`{"kind":"togglestep","entity":"step:pulse/1","shared":"pattern"}`, true, http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			path := filepath.Join(dir, "score.cicada")
			if err := os.WriteFile(path, []byte(c.score), 0600); err != nil {
				t.Fatal(err)
			}
			if c.stray {
				physical, err := filepath.EvalSymlinks(path)
				if err != nil {
					t.Fatal(err)
				}
				stray := filepath.Join(dir, strings.TrimSuffix(studioRecoveryPattern(physical), "*")+"stray")
				if err := os.WriteFile(stray, []byte("late write"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := newStudio(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.shutdown() })
			before := snapshotStudioState(t, s)
			revision := studioRevision(before.source)
			unchanged := func(what string) {
				t.Helper()
				after := snapshotStudioState(t, s)
				if !bytes.Equal(after.source, before.source) || after.history != before.history || after.log != before.log {
					t.Errorf("%s left a trace", what)
				}
			}
			if c.route != "" {
				response := studioCallRaw(t, s.routes(), c.route, fmt.Sprintf(c.body, revision))
				if response.Code != c.status {
					t.Errorf("route: %d, want %d: %s", response.Code, c.status, response.Body)
				}
				unchanged("route")
			}
			intents := fmt.Sprintf(`{"version":1,"revision":%q,"intents":[%s]}`, revision, c.intent)
			response := studioCallRaw(t, s.routes(), "/api/intents", intents)
			if response.Code < 400 {
				t.Errorf("/api/intents accepted a refused edit: %d %s", response.Code, response.Body)
			}
			unchanged("/api/intents")
			// The same batch behind a harmless first intent is refused whole.
			batch := fmt.Sprintf(`{"version":1,"revision":%q,"intents":[{"kind":"togglestep","entity":"step:pulse/1","shared":"pattern"},%s]}`, revision, c.intent)
			if c.stray {
				batch = intents
			}
			if response := studioCallRaw(t, s.routes(), "/api/intents", batch); response.Code < 400 {
				t.Errorf("batch accepted: %d %s", response.Code, response.Body)
			}
			unchanged("batch")
			file := filepath.Join(dir, "intents.json")
			if err := os.WriteFile(file, []byte(intents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := applyCommandWithHooks([]string{path, file, "--write"}, io.Discard, nil); err == nil {
				t.Error("cicada apply --write accepted a refused edit")
			}
			unchanged("cicada apply")
		})
	}
}

// An automation point edit changes the revision a take publishes against, so
// /api/automation refuses it during a recording (422). The intents behind that
// route, alone, in a batch, as a dry run, or staged before the recording and
// accepted during it, must refuse with the same status and text.
func TestAutomationPointEditsRefusedDuringCaptureOnEveryPath(t *testing.T) {
	const text = "finish the recording before editing automation"
	set := `{"kind":"setautomationpoint","entity":"param:bass.level","at":"@1.1.1","value":"-6dB"}`
	remove := `{"kind":"removeautomationpoint","entity":"param:bass.level","at":"@1.1.1"}`
	toggle := `{"kind":"togglestep","entity":"step:pulse/1","shared":"definition"}`
	point, _ := json.Marshal(studioCurvePoint{At: "@1.1.1", Value: "-6dB"})
	cases := []struct{ name, route, body string }{
		{"set route", "/api/automation", fmt.Sprintf(`{"revision":%%q,"action":"automation-point-set","path":"bass.level","value":%s}`, point)},
		{"remove route", "/api/automation", fmt.Sprintf(`{"revision":%%q,"action":"automation-point-remove","path":"bass.level","value":%s}`, point)},
		{"set intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + set + `]}`},
		{"remove intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + remove + `]}`},
		{"batch after another intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + toggle + `,` + set + `]}`},
		{"batch before another intent", "/api/intents", `{"version":1,"revision":%q,"intents":[` + remove + `,` + toggle + `]}`},
		{"dry run", "/api/intents", `{"version":1,"revision":%q,"dryrun":true,"intents":[` + set + `]}`},
		{"stage", "/api/proposals", `{"envelope":{"version":1,"revision":%q,"intents":[` + set + `]}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newTakeStudio(t, t.TempDir())
			id := activeVocalCapture(t, s)
			before := snapshotStudioState(t, s)
			response := studioCallRaw(t, s.routes(), c.route, fmt.Sprintf(c.body, studioRevision(before.source)))
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), text) {
				t.Fatalf("%d %s", response.Code, response.Body)
			}
			after := snapshotStudioState(t, s)
			if !bytes.Equal(after.source, before.source) || after.history != before.history || after.log != before.log {
				t.Fatal("refused edit left a trace")
			}
			s.mu.Lock()
			err := s.finishVocalCapture(id)
			s.mu.Unlock()
			if err != nil {
				t.Fatalf("take did not publish after refusal: %v", err)
			}
		})
	}
	t.Run("accept during capture", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		s := newTakeStudio(t, t.TempDir())
		before := snapshotStudioState(t, s)
		response := studioCallRaw(t, s.routes(), "/api/proposals", fmt.Sprintf(`{"envelope":{"version":1,"revision":%q,"intents":[`+set+`]}}`, studioRevision(before.source)))
		var staged struct {
			ID string `json:"id"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &staged) != nil || staged.ID == "" {
			t.Fatalf("stage: %d %s", response.Code, response.Body)
		}
		id := activeVocalCapture(t, s)
		response = studioCallRaw(t, s.routes(), "/api/proposals/"+staged.ID+"/accept", "{}")
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), text) {
			t.Fatalf("accept: %d %s", response.Code, response.Body)
		}
		after := snapshotStudioState(t, s)
		if !bytes.Equal(after.source, before.source) || after.history != before.history || after.log != before.log {
			t.Fatal("refused acceptance left a trace")
		}
		s.mu.Lock()
		err := s.finishVocalCapture(id)
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("take did not publish: %v", err)
		}
	})
}

// Audit of the other M10 intents: their routes never had a capture guard, so
// the intent must not add one. Route and intent answer alike during a take.
func TestM10RoutesAndIntentsAgreeDuringCapture(t *testing.T) {
	score := "cicada 2\ntempo 120\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\narrange {\n  place a bass pulse { at = @1.1.1 length = 4bars }\n  marker m { at = @3.1.1 }\n}\n"
	for _, c := range []struct{ name, route, body, intent string }{
		{"update placement", `{"revision":%q,"action":"update","name":"a","placement":{"length":"3bars"}}`, "", `{"kind":"updateplacement","entity":"placement:a","settings":{"length":"3bars"}}`},
		{"add placement", `{"revision":%q,"action":"append","newName":"b","placement":{"at":"@9.1.1","length":"1bars","track":"bass","content":"pulse"}}`, "", `{"kind":"addplacement","name":"b","settings":{"at":"@9.1.1","length":"1bars","track":"bass","content":"pulse"}}`},
		{"copy placement", `{"revision":%q,"action":"duplicate","name":"a","newName":"c","placement":{"at":"@9.1.1"}}`, "", `{"kind":"copyplacement","entity":"placement:a","name":"c","settings":{"at":"@9.1.1"}}`},
		{"set marker", `{"revision":%q,"action":"marker","name":"m","placement":{"at":"@4.1.1"}}`, "", `{"kind":"setmarker","entity":"marker:m","settings":{"at":"@4.1.1"}}`},
		{"add marker", `{"revision":%q,"action":"append-marker","newName":"n","placement":{"at":"@5.1.1"}}`, "", `{"kind":"addmarker","name":"n","settings":{"at":"@5.1.1"}}`},
		{"remove marker", `{"revision":%q,"action":"delete-marker","name":"m"}`, "", `{"kind":"removemarker","entity":"marker:m"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			status := func(route, body string) int {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				s := newTakeStudio(t, t.TempDir())
				if err := os.WriteFile(s.path, []byte(score), 0600); err != nil {
					t.Fatal(err)
				}
				activeVocalCapture(t, s)
				return studioCallRaw(t, s.routes(), route, fmt.Sprintf(body, studioRevision([]byte(score)))).Code
			}
			viaRoute := status("/api/arrange", c.route)
			viaIntent := status("/api/intents", `{"version":1,"revision":%q,"intents":[`+c.intent+`]}`)
			if viaRoute != viaIntent {
				t.Fatalf("route %d, intent %d", viaRoute, viaIntent)
			}
		})
	}
}
