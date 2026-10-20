package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
)

func TestRecordingSetupUsesAudioDialogInStudioAndLinksInStandalonePanels(t *testing.T) {
	address, client, _ := detailDockApp(t)
	for _, tc := range []struct {
		name, location, label, tag string
		attributes                 []string
	}{
		{"Studio setup", "/studio?tab=arrange&vocalTrack=harmony", "Choose microphone and output", "button", []string{`type="button"`, `data-gosx-disclosure-target="#audio-dialog"`, `aria-controls="audio-dialog"`}},
		{"Studio return", "/studio?tab=arrange&vocalTrack=harmony", "Back to recording", "button", []string{`type="button"`, `data-gosx-disclosure-close="#audio-dialog"`}},
		{"standalone setup", "/?panel=session&vocalTrack=harmony", "Choose microphone and output", "a", []string{`href="/?panel=audio&amp;returnTo=session&amp;vocalTrack=harmony"`}},
		{"standalone return", "/?panel=audio&returnTo=session&vocalTrack=harmony", "Back to recording", "a", []string{`href="/?panel=session&amp;vocalTrack=harmony"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := getPage(t, client, address+tc.location)
			matches := regexp.MustCompile(`<([a-z]+)\b[^>]*>`+regexp.QuoteMeta(tc.label)+`</[a-z]+>`).FindAllStringSubmatch(page, -1)
			if len(matches) != 1 || matches[0][1] != tc.tag {
				t.Fatalf("expected one %s for %q, got %v", tc.tag, tc.label, matches)
			}
			control := matches[0][0]
			for _, attribute := range tc.attributes {
				if !strings.Contains(control, attribute) {
					t.Errorf("control missing %s: %s", attribute, control)
				}
			}
			if tc.tag == "button" && strings.Contains(control, "href=") {
				t.Fatal("Studio audio dialog control must not navigate")
			}
		})
	}
}

func TestVocalLanesProjectWaveformsAndKeepAuditionRange(t *testing.T) {
	t.Setenv("CICADA_BACKEND_TOKEN", "wave-private")
	var command map[string]any
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer wave-private" {
			t.Error("private credential missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/workspace":
			_ = json.NewEncoder(w).Encode(workspace{Revision: "current", Project: &project.Project{Edition: 2, TempoMilli: 120000, Tracks: []project.Track{{ID: "vox", Kind: "audio"}, {ID: "harmony", Kind: "audio"}}, Song: []project.SongEntry{{Scene: "main", Bars: 8}}}})
		case "/api/transport":
			_, _ = io.WriteString(w, `{}`)
		case "/api/takes":
			if r.Method == "POST" {
				_ = json.NewDecoder(r.Body).Decode(&command)
				_, _ = io.WriteString(w, `{"revision":"current"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"takes": []takejournal.Take{{ID: "voice-a", Track: "harmony", Rate: 48000, Frames: 768000, Timeline: &takejournal.Timeline{StartTick: 0, EndTick: 30720, TempoMilli: 120000}, Asset: project.Asset{Path: "audio/blobs/private.wav"}}}})
		case "/api/takes/voice-a/waveform":
			if r.URL.RawQuery != "bins=8192" {
				t.Errorf("unexpected waveform query %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"take":"voice-a","startTick":0,"endTick":30720,"peaks":[[-0.5,0.5]],"peak":0.5}`)
		case "/api/takes/missing/waveform":
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":"take not found"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer audio.Close()
	b, _ := newBackend(audio.URL)
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	page := getPage(t, client, app.URL+"/?panel=session&vocalTrack=harmony")
	for _, want := range []string{"CicadaVocalLanes", "Chosen vocal", "data-vocal-wave=\"voice-a\"", "Hear selection", "Use selection", "Set punch range", "Exact positions", "Phrase gain (dB)", "Adjust phrase gain", "Reset gain", "Vocal only"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing lane control %s", want)
		}
	}
	if strings.Contains(page, "audio/blobs/private.wav") || strings.Contains(page, "wave-private") {
		t.Fatal("private path or credential leaked into lanes")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "takeId": {"voice-a"}, "timeline": {"on"}, "action": {"listen"}, "recordStart": {"@5.1.1"}, "recordEnd": {"@9.1.1"}, "__gosx_return_to": {"/?panel=session&vocalTrack=harmony"}}
	form.Set("gainDB", "-3.2")
	form.Set("auditionMode", "solo")
	response, err := client.PostForm(app.URL+"/__actions/take", form)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || response.Request.URL.Query().Get("vocalTrack") != "harmony" {
		t.Fatal("audition lost the selected vocal track")
	}
	recording := command["recording"].(map[string]any)
	if recording["start"] != "@5.1.1" || recording["end"] != "@9.1.1" || command["takeId"] != "voice-a" {
		t.Fatalf("wrong phrase request %+v", command)
	}
	if recording["gainDB"] != -3.2 || recording["auditionMode"] != "solo" {
		t.Fatalf("phrase level/mode lost: %+v", recording)
	}
	if !strings.Contains(string(data), `value="-3.2"`) || !strings.Contains(string(data), `value="solo" selected`) {
		t.Fatal("audition forgot its level or vocal-only mode")
	}
	if !strings.Contains(string(data), `value="@5.1.1"`) {
		t.Fatal("audition forgot selected phrase")
	}
	response, err = client.Get(app.URL + "/api/takes/voice-a/waveform?bins=8192&ignore=anything")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(data), `"peaks"`) {
		t.Fatal("waveform was not projected")
	}
	response, err = client.Get(app.URL + "/api/takes/missing/waveform")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("waveform failure lost status: %d", response.StatusCode)
	}
}
