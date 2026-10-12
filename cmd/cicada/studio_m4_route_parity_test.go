package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type m4RouteCase struct {
	name, route, source string
	body                studioEdit
	edition             int
	part                bool
	wantStatus          int
	wantError           string
}

func m4RouteCases() []m4RouteCase {
	var cases []m4RouteCase
	add := func(route, name, source string, body studioEdit, status int, message string) {
		cases = append(cases, m4RouteCase{name: route[5:] + "/" + name, route: route, source: source, body: body, wantStatus: status, wantError: message})
	}
	for _, c := range []struct {
		name    string
		body    studioEdit
		status  int
		message string
	}{
		{"move forward", studioEdit{Action: "move", Target: 2}, 200, ""}, {"move backward", studioEdit{Action: "move", Index: 2}, 200, ""}, {"move unchanged", studioEdit{Action: "move", Index: 1, Target: 1}, 200, ""},
		{"bars explicit", studioEdit{Action: "bars", Bars: 4}, 200, ""}, {"bars implicit", studioEdit{Action: "bars", Index: 1, Bars: 4}, 200, ""}, {"bars unchanged", studioEdit{Action: "bars", Index: 1, Bars: 1}, 200, ""},
		{"append", studioEdit{Action: "append", Scene: "chorus", Bars: 2}, 200, ""}, {"duplicate", studioEdit{Action: "duplicate", Index: 1}, 200, ""}, {"delete", studioEdit{Action: "delete", Index: 1}, 200, ""}, {"scene", studioEdit{Action: "scene", Scene: "chorus"}, 200, ""},
		{"bad index", studioEdit{Action: "bars", Index: 5, Bars: 4}, 422, "song entry is out of range"}, {"bad target", studioEdit{Action: "move", Target: 5}, 422, "song destination is out of range"}, {"bad bars", studioEdit{Action: "bars"}, 422, "song entry must last 1–999 bars"}, {"bad scene", studioEdit{Action: "scene", Scene: "missing"}, 422, "choose an existing scene"}, {"unknown", studioEdit{Action: "bad"}, 400, "unknown arrangement action"}, {"label", studioEdit{Action: "move", Target: 1, Label: "Authored song gesture"}, 200, ""},
	} {
		add("/api/song", c.name, studioSongScore, c.body, c.status, c.message)
	}
	commented := strings.Replace(studioSongScore, "  dusk*2\n", "  // verse\n  dusk*2 // the hook\n", 1)
	for _, action := range []string{"move", "append", "duplicate", "delete", "bars", "scene"} {
		status, message := 422, "song comments need a source edit to preserve their attachment"
		if action == "bars" || action == "scene" {
			status, message = 200, ""
		}
		add("/api/song", "comment "+action, commented, studioEdit{Action: action, Target: 1, Scene: "chorus", Bars: 4}, status, message)
	}
	add("/api/song", "delete last", strings.Replace(studioSongScore, "  dusk*2\n  chorus\n  dusk*3", "  dusk", 1), studioEdit{Action: "delete"}, 422, "keep at least one arrangement block")
	add("/api/song", "multiplier spacing", strings.Replace(studioSongScore, "dusk*2", "dusk * 2", 1), studioEdit{Action: "bars", Bars: 7}, 200, "")
	for _, c := range []struct {
		name, scene, path, value, action string
		status                           int
		message                          string
	}{
		{"set", "main", "bass.cutoff", `"1.25kHz"`, "automation-set", 200, ""}, {"insert precision", "hold", "bass.level", `-6.25`, "automation-set", 200, ""}, {"remove", "main", "bass.cutoff", "", "automation-remove", 200, ""},
		{"missing point", "hold", "bass.pan", "", "automation-remove", 422, "scene hold has no point for bass.pan"}, {"out of range", "main", "bass.cutoff", `50000`, "automation-set", 422, ""}, {"unit mismatch", "main", "bass.level", `"2Hz"`, "automation-set", 422, ""},
		{"forbidden", "main", "bass.insert", `"none"`, "automation-set", 422, "bass.insert cannot be automated at a scene boundary"}, {"tempo", "main", "tempo", `140`, "automation-set", 422, "tempo cannot be automated at a scene boundary"},
		{"unknown path", "main", "unknown.pan", `0`, "automation-set", 422, ""}, {"missing scene", "missing", "bass.pan", `0`, "automation-set", 422, `unknown declaration "missing"`}, {"missing value", "main", "bass.pan", "", "automation-set", 422, ""}, {"unknown", "main", "bass.pan", `0`, "bad", 400, "choose an automation operation"},
	} {
		add("/api/automation", c.name, automationScore, studioEdit{Action: c.action, Scene: c.scene, Path: c.path, Value: json.RawMessage(c.value)}, c.status, c.message)
	}
	projectSource := "cicada 2\n// authored header\nkey db minor\ntempo // tempo comment\n138.0\ntrack bass acid {}\npattern p { 1 . c#3 . }\nscene main { bass=p }\nsong { main }\n"
	settings := &studioProjectSettings{Title: "東京 \"Studio\"", TempoMilli: 127125, Root: "c#", Scale: "minor"}
	add("/api/project", "spelling and precision", projectSource, studioEdit{Metadata: settings}, 200, "")
	add("/api/project", "defaults", studioScore, studioEdit{Metadata: &studioProjectSettings{Title: "New", TempoMilli: 120000, Root: "a", Scale: "minor"}}, 200, "")
	add("/api/project", "unchanged", studioScore, studioEdit{Metadata: &studioProjectSettings{Title: "Studio", TempoMilli: 130000, Root: "a", Scale: "minor"}}, 200, "")
	add("/api/project", "missing", studioScore, studioEdit{}, 422, "choose a title of 1–256 characters and tempo 20–300 BPM")
	for _, c := range []struct {
		name     string
		settings studioProjectSettings
		message  string
	}{
		{"blank title", studioProjectSettings{Title: " ", TempoMilli: 120000, Root: "a", Scale: "minor"}, "choose a title of 1–256 characters and tempo 20–300 BPM"},
		{"long title", studioProjectSettings{Title: strings.Repeat("a", 257), TempoMilli: 120000, Root: "a", Scale: "minor"}, "choose a title of 1–256 characters and tempo 20–300 BPM"},
		{"tempo", studioProjectSettings{Title: "New", TempoMilli: 19000, Root: "a", Scale: "minor"}, "choose a title of 1–256 characters and tempo 20–300 BPM"},
		{"key", studioProjectSettings{Title: "New", TempoMilli: 120000, Root: "db", Scale: "minor"}, "choose a supported key and scale"},
		{"scale", studioProjectSettings{Title: "New", TempoMilli: 120000, Root: "a", Scale: "bad"}, "choose a supported key and scale"},
	} {
		add("/api/project", c.name, studioScore, studioEdit{Metadata: &c.settings}, 422, c.message)
	}
	add("/api/project", "retuning", "key a minor\ntrack bass acid {}\npattern p { 2 6 }\nscene main { bass=p }\nsong { main }\n", studioEdit{Metadata: &studioProjectSettings{Title: "Test", TempoMilli: 120000, Root: "c", Scale: "pent"}}, 422, "")
	clipSource, _ := m4ClipSource()
	for _, c := range []struct {
		name    string
		body    studioEdit
		status  int
		message string
	}{
		{"track", studioEdit{Action: "audio-track", NewName: "vocal-b"}, 200, ""}, {"duplicate track", studioEdit{Action: "audio-track", NewName: "vox"}, 422, "track vox already exists"}, {"bad name", studioEdit{Action: "audio-track", NewName: "bad\ntrack x"}, 422, "choose a lowercase audio track name in an edition-2 score"},
		{"fades", studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 480, End: 4800, GainDB: -2, FadeIn: 48, FadeOut: 96}}, 200, ""},
		{"region", studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 960, End: 9600, GainDB: -4}}, 200, ""},
		{"unchanged", studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 480, End: 4800, GainDB: -2}}, 200, ""},
		{"past asset", studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 480, End: 50000}}, 422, "clip end exceeds the source asset"},
		{"bad fades", studioEdit{Action: "clip-settings", Pattern: "hit", ClipSettings: &studioClipSettings{Start: 480, End: 481, FadeIn: 48}}, 422, "choose a valid source region, fades no longer than the region, and gain −60 to +24 dB"},
		{"missing settings", studioEdit{Action: "clip-settings", Pattern: "hit"}, 422, "choose a valid source region, fades no longer than the region, and gain −60 to +24 dB"},
		{"missing clip", studioEdit{Action: "clip-settings", Pattern: "ghost", ClipSettings: &studioClipSettings{Start: 480, End: 4800}}, 422, "unknown clip ghost"},
		{"bind", studioEdit{Action: "clip-bind", Scene: "main", Track: "vox", Pattern: "off"}, 200, ""}, {"bind unchanged", studioEdit{Action: "clip-bind", Scene: "main", Track: "vox", Pattern: "hit"}, 200, ""}, {"bind missing scene", studioEdit{Action: "clip-bind", Scene: "ghost", Track: "vox", Pattern: "hit"}, 422, `unknown declaration "ghost"`}, {"unknown", studioEdit{Action: "bad"}, 400, "unknown audio clip action"},
	} {
		add("/api/clip", c.name, clipSource, c.body, c.status, c.message)
	}
	add("/api/clip", "edition1", studioScore, studioEdit{Action: "audio-track", NewName: "new"}, 422, "Audio tracks require edition 2. Use the Mixer upgrade option first.")
	for _, route := range []string{"/api/source", "/api/files"} {
		for _, c := range []struct {
			name, source string
			status       int
		}{
			{"write", strings.Replace(studioScore, `title "Studio"`, `title "Saved"`, 1), 200}, {"unchanged", studioScore, 200}, {"invalid", "title \"invalid\"\n???\n", 422},
		} {
			add(route, c.name, studioScore, studioEdit{Source: c.source, File: "score.cicada"}, c.status, "")
		}
	}
	add("/api/files", "unknown file", studioScore, studioEdit{Source: studioScore, File: "../other.cicada"}, 400, "file is not a project source")
	// Manifest inheritance and loose authored headers use the M3 edition policy.
	for _, route := range []string{"/api/song", "/api/automation", "/api/project", "/api/clip", "/api/source"} {
		for _, layout := range []string{"loose edition2", "manifest edition2", "inherited edition2"} {
			source, body := "cicada 2\n"+studioSongScore, studioEdit{Action: "bars", Bars: 4}
			switch route {
			case "/api/automation":
				source, body = "cicada 2\n"+automationScore, studioEdit{Action: "automation-set", Scene: "hold", Path: "bass.pan", Value: json.RawMessage(`0.375`)}
			case "/api/project":
				source, body = projectSource, studioEdit{Metadata: settings}
			case "/api/clip":
				source, body = clipSource, studioEdit{Action: "audio-track", NewName: "vocal-b"}
			case "/api/source":
				source, body = "cicada 2\n"+studioScore, studioEdit{Source: "cicada 2\n" + strings.Replace(studioScore, `title "Studio"`, `title "Saved"`, 1)}
			}
			edition := 0
			if layout != "loose edition2" {
				edition = 2
			}
			if layout == "inherited edition2" {
				source = strings.TrimPrefix(source, "cicada 2\n")
				body.Source = strings.TrimPrefix(body.Source, "cicada 2\n")
			}
			cases = append(cases, m4RouteCase{name: route[5:] + "/" + layout, route: route, source: source, body: body, edition: edition, wantStatus: 200})
		}
	}
	for _, c := range []struct {
		name, source string
		status       int
	}{
		{"part write", "pattern riff { 2 . 6 . }\n", 200}, {"part unchanged", "pattern riff { 1 . 5 . }\n", 200}, {"part invalid", "pattern riff { nonsense }\n", 422},
	} {
		cases = append(cases, m4RouteCase{name: "files/" + c.name, route: "/api/files", source: "pattern riff { 1 . 5 . }\n", body: studioEdit{File: "part.cicada", Source: c.source}, part: true, wantStatus: c.status})
	}
	var expanded []m4RouteCase
	for _, c := range cases {
		for _, newline := range []string{"\n", "\r\n"} {
			v := c
			v.name += fmt.Sprintf("/%q", newline)
			v.source, v.body.Source = strings.ReplaceAll(c.source, "\n", newline), strings.ReplaceAll(c.body.Source, "\n", newline)
			expanded = append(expanded, v)
		}
	}
	return expanded
}

func m4Session(t *testing.T, c m4RouteCase) (*studio, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := studioTestPath(t, c.source)
	entry := path
	if c.part {
		entry = filepath.Join(filepath.Dir(path), "main.cicada")
		path = filepath.Join(filepath.Dir(path), "part.cicada")
		for name, text := range map[string]string{
			"cicada.mod":  "project parts\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n",
			"main.cicada": "track bass acid {}\nscene main { bass=riff }\nsong { main }\n",
			"part.cicada": c.source,
		} {
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
	} else if c.edition != 0 {
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cicada.mod"), []byte(fmt.Sprintf("project edits\ncicada %d\n", c.edition)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(c.source, "asset take") {
		_, wav := m4ClipSource()
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), "take.wav"), wav, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(entry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.shutdown() })
	return s, path
}

func m4Route(t *testing.T, c m4RouteCase) gridPatternRouteSnapshot {
	t.Helper()
	s, path := m4Session(t, c)
	body := c.body
	body.Revision = studioRevision([]byte(c.source))
	response := studioCall(t, s.routes(), c.route, body)
	reply := gridPatternRouteSnapshot{Code: response.Code}
	if err := json.Unmarshal(response.Body.Bytes(), &reply.Body); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reply.Source = string(after)
	if preserved, ok := reply.Body["preserved"].(string); ok && preserved != "" {
		before, err := os.ReadFile(preserved)
		if err != nil || !bytes.Equal(before, []byte(c.source)) {
			t.Fatalf("preserved bytes %q: %v", before, err)
		}
		reply.Body["preserved"] = "<preserved-score>"
	}
	target := s
	if c.part {
		target = s.fileSessions[path]
	}
	if target != nil {
		for _, e := range target.history.historySnapshot().Edits {
			reply.Labels = append(reply.Labels, e.Label)
		}
	}
	return reply
}

// This guard also runs during capture: a case intended to succeed must never
// turn an environmental or writer error into its golden expectation.
func assertM4Capture(t *testing.T, c m4RouteCase, reply gridPatternRouteSnapshot) {
	t.Helper()
	if reply.Code != c.wantStatus {
		t.Fatalf("%s: expected %d, got %d: %+v", c.name, c.wantStatus, reply.Code, reply.Body)
	}
	if c.wantStatus < 400 {
		if reply.Body["error"] != nil || reply.Body["valid"] != true {
			t.Fatalf("%s: expected valid success: %+v", c.name, reply.Body)
		}
	} else {
		if message, ok := reply.Body["error"].(string); !ok || message == "" || c.wantError != "" && message != c.wantError {
			t.Fatalf("%s: intended error %q, got %+v", c.name, c.wantError, reply.Body)
		}
		if reply.Source != c.source || len(reply.Labels) != 0 {
			t.Fatalf("%s: error changed source/history: %+v", c.name, reply)
		}
	}
}

func m4Captures(t *testing.T) map[string]gridPatternRouteSnapshot {
	t.Helper()
	data, err := os.ReadFile("testdata/m4-route-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]gridPatternRouteSnapshot
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

func TestM4CaptureInventory(t *testing.T) {
	golden := m4Captures(t)
	cases := m4RouteCases()
	if len(golden) != len(cases) {
		t.Fatalf("%d captures, %d cases", len(golden), len(cases))
	}
	var successes, errors int
	for _, c := range cases {
		assertM4Capture(t, c, golden[c.name])
		if c.wantStatus < 400 {
			successes++
		} else {
			errors++
		}
	}
	t.Logf("legacy route captures: %d successes, %d intended errors", successes, errors)
}

func assertM4RouteParity(t *testing.T, route string) {
	t.Helper()
	golden := m4Captures(t)
	for _, c := range m4RouteCases() {
		if c.route != route {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			want, ok := golden[c.name]
			if !ok {
				t.Fatal("missing legacy capture")
			}
			assertM4Capture(t, c, want)
			if strings.HasPrefix(c.name, "song/comment move/") {
				// M4's deliberate parity exception: comments move with the entry.
				newline := "\n"
				if strings.Contains(c.source, "\r\n") {
					newline = "\r\n"
				}
				before := strings.Join([]string{"  // verse", "  dusk*2 // the hook", "  chorus"}, newline)
				after := strings.Join([]string{"  chorus", "  // verse", "  dusk*2 // the hook"}, newline)
				want.Source = strings.Replace(c.source, before, after, 1)
				want.Code, want.Labels = 200, []string{"Song block 1 moved to 2"}
				want.Body = map[string]any{"valid": true, "revision": studioRevision([]byte(want.Source)), "playingRevision": studioRevision([]byte(want.Source)), "source": want.Source, "preserved": "<preserved-score>"}
				c.wantStatus, c.wantError = 200, ""
			}
			got := m4Route(t, c)
			assertM4Capture(t, c, got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("route parity\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestRouteParity_Song(t *testing.T)       { assertM4RouteParity(t, "/api/song") }
func TestRouteParity_Automation(t *testing.T) { assertM4RouteParity(t, "/api/automation") }
func TestRouteParity_Project(t *testing.T)    { assertM4RouteParity(t, "/api/project") }
func TestRouteParity_Clip(t *testing.T)       { assertM4RouteParity(t, "/api/clip") }
func TestRouteParity_Source(t *testing.T)     { assertM4RouteParity(t, "/api/source") }
func TestRouteParity_Files(t *testing.T)      { assertM4RouteParity(t, "/api/files") }
