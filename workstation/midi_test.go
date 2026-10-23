package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/host/midi"
)

func TestNativeMIDIProxyRequiresCSRFStreamsAndCancels(t *testing.T) {
	var opened atomic.Int32
	closed := make(chan struct{}, 1)
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/workspace" {
			_, _ = w.Write([]byte(`{"revision":"midi-test"}`))
			return
		}
		if r.URL.Path != "/api/midi/input" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer midi-test-private-token" {
			t.Error("native MIDI lost its private service credential")
			w.WriteHeader(401)
			return
		}
		opened.Add(1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(midi.Event{Type: "devices", Inputs: []midi.Device{{ID: "coremidi:1", Name: "Virtual controller"}}})
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		closed <- struct{}{}
	}))
	defer audio.Close()
	b, err := newBackend(audio.URL)
	if err != nil {
		t.Fatal(err)
	}
	b.token = "midi-test-private-token"
	handler, err := newApp(b)
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(handler)
	defer app.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := tokenFromPage(t, getPage(t, client, app.URL+"/?panel=live"))
	b.client.Timeout = 20 * time.Millisecond
	post := func(ctx context.Context, token, origin string) *http.Response {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, app.URL+"/api/midi/input", bytes.NewBufferString("{}"))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		request.Header.Set("X-CSRF-Token", token)
		request.Header.Set("Origin", origin)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, bad := range [][2]string{{"", app.URL}, {csrf, "https://elsewhere.example"}} {
		response := post(context.Background(), bad[0], bad[1])
		response.Body.Close()
		if response.StatusCode != 403 || opened.Load() != 0 {
			t.Fatal("untrusted request opened MIDI", response.StatusCode)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response := post(ctx, csrf, app.URL)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	var event midi.Event
	if err := json.NewDecoder(response.Body).Decode(&event); err != nil || event.Type != "devices" || len(event.Inputs) != 1 || event.Inputs[0].Name != "Virtual controller" {
		t.Fatalf("MIDI event: %+v %v", event, err)
	}
	for key, values := range response.Header {
		if strings.Contains(strings.Join(values, " "), b.token) {
			t.Fatal("credential exposed in header", key)
		}
	}
	time.Sleep(60 * time.Millisecond) // The stream outlives ordinary request deadlines.
	select {
	case <-closed:
		t.Fatal("ordinary timeout closed MIDI")
	default:
	}
	cancel()
	response.Body.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("browser disconnect retained the backend stream")
	}
	if b.client.Timeout != 20*time.Millisecond {
		t.Fatal("MIDI changed ordinary request timeout")
	}
}
