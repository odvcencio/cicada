package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"m31labs.dev/gosx"
)

func TestAudioPermissionWaitSurvivesOrdinaryTimeoutAndStillCancels(t *testing.T) {
	for _, outcome := range []string{"granted", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				select {
				case <-release:
					io.WriteString(w, `{}`)
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			defer close(release)
			b, err := newBackend(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			b.client.Timeout = 20 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- b.call(ctx, http.MethodPost, "/api/audio/config", audioOptions{InputEnabled: true}, nil)
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("configuration did not reach the service")
			}
			select {
			case err := <-done:
				t.Fatalf("permission wait ended before a human response: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if outcome == "canceled" {
				cancel()
			} else {
				release <- struct{}{}
			}
			select {
			case err := <-done:
				if outcome == "canceled" && !errors.Is(err, context.Canceled) || outcome == "granted" && err != nil {
					t.Fatalf("permission result lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("configuration did not finish")
			}
			if b.client.Timeout != 20*time.Millisecond {
				t.Fatal("permission wait changed ordinary request timeout")
			}
		})
	}
}

func TestAudioSetupRetainsDisconnectedDeviceAndReturnsToVocalTrack(t *testing.T) {
	var applied audioOptions
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			io.WriteString(w, `{ "revision":"current" }`)
			return
		}
		if r.URL.Path == "/api/audio/config" {
			if r.Method == "POST" {
				json.NewDecoder(r.Body).Decode(&applied)
				io.WriteString(w, `{}`)
			} else {
				io.WriteString(w, `{"settingsWarning":"Saved audio setup is unreadable. Defaults are in use.","inventory":{"supported":true,"backend":"Tymbal CoreAudio","backendName":"tymbal","host":"coreaudio","devices":[{"id":"mic-live","name":"Webcam microphone","inputs":1,"defaultInput":true},{"id":"speakers-live","name":"Headphones","outputs":2}]},"options":{"inputDevice":"mic-unplugged","outputDevice":"","inputEnabled":true,"monitorMuted":true,"monitorMode":"mono1","monitorGain":0.5,"ampEnabled":true,"ampDrive":0.4,"ampTone":0.6,"ampOutput":-3,"ampCabinet":2,"recordingOffsetMS":17.5,"periodFrames":128},"runtime":{"requestedPeriodFrames":128,"periodFrames":240,"sampleRate":48000,"inputLatencyMs":3,"inputLatencyKnown":true,"outputLatencyMs":4,"outputLatencyKnown":true,"reportedRoundTripMs":7,"reportedRoundTripKnown":true,"audioPriority":"mmcss","deadlineKnown":true,"callbacks":42,"callbackMaxUs":2000,"callbackBudgetPercent":40,"warning":"Device negotiated 240 frames for the requested 128-frame buffer."}}`)
			}
			return
		}
		io.WriteString(w, `{}`)
	}))
	defer backend.Close()
	b, _ := newBackend(backend.URL)
	h, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(h)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page := getPage(t, client, app.URL+"/?panel=audio&returnTo=session&vocalTrack=danilo")
	for _, want := range []string{"Saved audio setup is unreadable. Defaults are in use.", "New sessions start stopped", "Choose a playback output", "Headphones", "Webcam microphone", "Saved device (disconnected)", "Apply and return to recording", "Guitar amp and cabinet", "Enable guitar amp for monitoring", "Amp output (dB)", "Bright", "Back to recording", "Refresh devices", "Audio details", "Buffer size", "Requested buffer", "Granted buffer", "240 frames", "Reported round trip", "7.00 ms", "mmcss", "40.0%", "hardware loopback measurement", "Device negotiated 240 frames", "macOS will ask for microphone access", "Privacy &amp; Security", "allow Cicada Studio"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing audio setup guidance %q", want)
		}
	}
	if !strings.Contains(page, `value="mic-unplugged" selected`) && !strings.Contains(page, `selected value="mic-unplugged"`) {
		t.Fatal("unplugged microphone silently became the default")
	}
	form := url.Values{"csrf_token": {tokenFromPage(t, page)}, "revision": {"current"}, "inputDevice": {"mic-live"}, "outputDevice": {"speakers-live"}, "inputEnabled": {"on"}, "monitorMuted": {"on"}, "monitorGain": {"0.5"}, "monitorMode": {"mono1"}, "recordingOffsetMS": {"17.5"}, "ampEnabled": {"on"}, "ampDrive": {"0.4"}, "ampTone": {"0.6"}, "ampOutput": {"-3"}, "ampCabinet": {"2"}, "periodFrames": {"64"}, "__gosx_return_to": {"/?panel=session&vocalTrack=danilo"}}
	response, err := client.PostForm(app.URL+"/__actions/audio", form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 || !strings.Contains(response.Header.Get("Location"), "vocalTrack=danilo") || applied.InputDevice != "mic-live" || applied.OutputDevice != "speakers-live" || !applied.InputEnabled || !applied.MonitorMuted || applied.PeriodFrames != 64 || applied.RecordingOffsetMS != 17.5 || !applied.AmpEnabled || applied.AmpDrive != .4 || applied.AmpTone != .6 || applied.AmpOutput != -3 || applied.AmpCabinet != 2 {
		t.Fatalf("audio return/settings lost: %d %s %+v", response.StatusCode, response.Header.Get("Location"), applied)
	}
	// Enhanced forms must honor the same explicit destination, instead of
	// rebuilding the Audio panel at the submitted current location.
	response, result := reactivePost(t, client, app.URL, "audio", map[string]string{
		"csrf_token": tokenFromPage(t, page), "revision": "current", "inputDevice": "mic-live", "outputDevice": "speakers-live", "inputEnabled": "on", "monitorMuted": "on", "monitorGain": "0.5", "monitorMode": "mono1",
		"recordingOffsetMS": "17.5", "periodFrames": "64",
		"__cicada_location": "/?panel=audio&returnTo=session&vocalTrack=danilo", "__gosx_return_to": "/?panel=session&vocalTrack=danilo",
	})
	var projection workspaceProjection
	json.Unmarshal(result.Data, &projection)
	if response.StatusCode != 200 || !result.OK || projection.Location != "/?panel=session&vocalTrack=danilo" {
		t.Fatalf("enhanced Audio lost Session return: %d %+v %+v", response.StatusCode, result, projection)
	}
	if applied.PeriodFrames != 64 || applied.RecordingOffsetMS != 17.5 {
		t.Fatalf("enhanced Audio lost buffer/timing: %+v", applied)
	}
	// Opening Audio in Studio does not navigate, so its existing form must
	// retain the vocal track without relying on the legacy returnTo query.
	studioLocation := "/studio?tab=arrange&dock=automation&vocalTrack=harmony"
	studioPage := getPage(t, client, app.URL+studioLocation)
	audioForm := regexp.MustCompile(`(?s)<form[^>]*action="/__actions/audio"[^>]*>.*?</form>`).FindString(studioPage)
	if !strings.Contains(audioForm, `name="__gosx_return_to" value="/?panel=session&amp;vocalTrack=harmony"`) {
		t.Fatal("Studio Audio form lost the selected vocal track")
	}
	form.Set("csrf_token", tokenFromPage(t, studioPage))
	form.Set("__gosx_return_to", "/?panel=session&vocalTrack=harmony")
	response = shellPost(t, client, app.URL, "/__actions/audio", form, app.URL+studioLocation)
	if want := "/studio?dock=automation&tab=arrange&vocalTrack=harmony"; response.StatusCode != 303 || response.Header.Get("Location") != want {
		t.Fatalf("Studio Audio lost recording context: %d %s", response.StatusCode, response.Header.Get("Location"))
	}
	for _, period := range []string{"0", "63", "480", "8192", "64.5", "bad"} {
		previous := applied
		_, result := reactivePost(t, client, app.URL, "audio", map[string]string{"csrf_token": tokenFromPage(t, page), "revision": "current", "periodFrames": period, "monitorGain": "0.5", "monitorMode": "stereo"})
		if result.OK || applied != previous {
			t.Fatalf("invalid buffer %q reached native backend: %+v %+v", period, result, applied)
		}
	}
}

func TestRecordingReadinessDistinguishesMicOffMissingOutputAndSilentBackend(t *testing.T) {
	var s audioState
	json.Unmarshal([]byte(`{"inventory":{"supported":true,"backend":"Tymbal WASAPI shared mode","backendName":"tymbal","devices":[{"id":"mic","name":"Webcam","inputs":1,"defaultInput":true},{"id":"out","name":"Headphones","outputs":2,"defaultOutput":true}]},"options":{"inputEnabled":false}}`), &s)
	if r := recordingReadiness(s); r.Title != "Microphone off" || r.Ready {
		t.Fatal(r)
	}
	if !strings.Contains(gosx.RenderHTML(audioConnections(s)), `data-input-ready="false"`) {
		t.Fatal("disabled input was shown as a configured recording path")
	}
	s.Options.InputEnabled = true
	if r := recordingReadiness(s); !r.Ready || r.Input != "Webcam" || r.Output != "Headphones" {
		t.Fatal(r)
	}
	if !strings.Contains(gosx.RenderHTML(audioConnections(s)), `data-input-ready="true"`) {
		t.Fatal("configured native input was not connected in the audio view")
	}
	s.Options.OutputDevice = "disconnected"
	if r := recordingReadiness(s); r.Title != "Choose a playback output" || r.Ready || r.PlaybackReady {
		t.Fatal(r)
	}
	s.Inventory.BackendName = "null"
	if r := recordingReadiness(s); r.Title != "Silent playback" || r.Ready || r.PlaybackReady {
		t.Fatal(r)
	}
}

func TestOutputOnlyNativeDriverKeepsPlaybackSelectableWithoutPromisingRecording(t *testing.T) {
	var state audioState
	err := json.Unmarshal([]byte(`{"inventory":{"supported":true,"captureSupported":false,"backend":"Tymbal Core Audio","backendName":"tymbal","devices":[{"id":"mic","name":"Built-in microphone","inputs":1,"defaultInput":true},{"id":"out","name":"Studio headphones","outputs":2,"defaultOutput":true}]},"options":{"inputEnabled":false}}`), &state)
	if err != nil {
		t.Fatal(err)
	}
	r := recordingReadiness(state)
	if r.Ready || !r.PlaybackReady || r.Title != "Playback configured" || r.Output != "Studio headphones" || r.Input != "Recording unavailable" {
		t.Fatalf("misleading readiness: %+v", r)
	}
	page := gosx.RenderHTML(audioConnections(state))
	if !strings.Contains(page, `data-input-ready="false"`) || !strings.Contains(page, `data-output-ready="true"`) || !strings.Contains(page, "Studio headphones") || !strings.Contains(page, "Recording input is not available") {
		t.Fatalf("connection graph misrepresents output-only driver: %s", page)
	}
	state.Options.InputEnabled = true
	if recordingReadiness(state).Ready {
		t.Fatal("saved input setting bypassed native capture capability")
	}
}

func TestMicrophonePermissionNeverClaimsRecordingOrDisconnectsPlayback(t *testing.T) {
	var state audioState
	if err := json.Unmarshal([]byte(`{"inventory":{"supported":true,"captureSupported":true,"backendName":"tymbal","devices":[{"id":"mic","name":"Built-in microphone","inputs":1,"defaultInput":true},{"id":"out","name":"Studio headphones","outputs":2,"defaultOutput":true}]},"options":{"inputEnabled":true}}`), &state); err != nil {
		t.Fatal(err)
	}
	for _, access := range []string{"prompt", "denied", "restricted", "unavailable"} {
		state.Inventory.InputAccess = access
		r := recordingReadiness(state)
		page := gosx.RenderHTML(audioConnections(state))
		if r.Ready || !r.PlaybackReady || !strings.Contains(page, `data-input-ready="false"`) || !strings.Contains(page, `data-output-ready="true"`) || strings.Contains(page, "Configured for recording") {
			t.Fatalf("%s permission misrepresented: %+v %s", access, r, page)
		}
	}
	state.Inventory.InputAccess = "granted"
	if r := recordingReadiness(state); !r.Ready || !r.PlaybackReady {
		t.Fatal(r)
	}
}
