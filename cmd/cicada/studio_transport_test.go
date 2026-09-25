package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"m31labs.dev/cicada/host/liveplay"
)

func TestStudioTransportSocketAndIdleCommand(t *testing.T) {
	handler, _ := studioTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/transport/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	var state transportSnapshot
	if err := wsjson.Read(ctx, connection, &state); err != nil {
		t.Fatal(err)
	}
	if state.Type != "cicada/transport" || state.Playing || state.Bar != 1 || state.Step != 1 {
		t.Fatalf("idle transport snapshot: %+v", state)
	}
	body := bytes.NewBufferString(`{"action":"stop"}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/transport", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://outside.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin command: %d", response.StatusCode)
	}
	idle := studioCall(t, handler, "/api/transport", struct {
		Action string `json:"action"`
	}{Action: "stop"})
	if idle.Code != http.StatusOK {
		t.Fatalf("idle stop: %d %s", idle.Code, idle.Body.String())
	}
}

func TestStudioTransportQueuesFileEditsOnNativeBar(t *testing.T) {
	_, path := studioTestHandler(t)
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.last = stream, fingerprint
	transport.history = newStudioHistory([]byte(studioScore))
	updated := strings.Replace(studioScore, "Studio", "Next Studio", 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	transport.poll()
	if !transport.snapshot().Pending {
		t.Fatal("validated file edit was not queued")
	}
	if events := transport.history.snapshot(); len(events) != 2 || events[0].Kind != "queued" {
		t.Fatalf("queue missing from history: %+v", events)
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Bar != 2 || event.Name != path {
			t.Fatalf("wrong edit landing: %+v", event)
		}
		transport.markLanded(event)
	default:
		t.Fatal("edit did not land at next bar")
	}
	if events := transport.history.snapshot(); len(events) != 3 || events[0].Kind != "landed" || events[0].Bar != 2 {
		t.Fatalf("landing missing from history: %+v", events)
	}
	if position := stream.Position(); position.Bar != 2 {
		t.Fatalf("native transport position: %+v", position)
	}
}

func TestStudioSceneLaunchUsesNativeTransport(t *testing.T) {
	_, path := studioTestHandler(t)
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.playing = stream, true
	transport.last, err = playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	transport.history = newStudioHistory([]byte(studioScore))
	// Exercise the HTTP command against an active stream without opening an audio device.
	p, err := compileStudioSource(path, []byte(studioScore))
	if err != nil {
		t.Fatal(err)
	}
	studio := &studio{path: path, lastGoodSource: []byte(studioScore), lastGoodProject: p, transport: transport}
	revision := studioRevision([]byte(studioScore))
	request := httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"launch","scene":"main","revision":"`+revision+`"}`))
	response := httptest.NewRecorder()
	studio.transportCommand(response, request)
	if response.Code != http.StatusOK || transport.snapshot().PendingScene != "main" {
		t.Fatalf("scene command: %d %s", response.Code, response.Body.String())
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		transport.markLanded(event)
	default:
		t.Fatal("scene launch did not reach native player")
	}
	state := transport.snapshot()
	if state.PendingScene != "" || state.Scene != "main" || state.Landed != 2 {
		t.Fatalf("landed scene state: %+v", state)
	}
	if events := transport.history.snapshot(); len(events) != 3 || events[0].Kind != "landed" {
		t.Fatalf("scene launch history: %+v", events)
	}
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"launch","scene":"","revision":"`+revision+`"}`)))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty scene: %d", response.Code)
	}
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"launch","scene":"main","revision":"stale"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale scene page: %d", response.Code)
	}
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"slot","track":"bass","pattern":"pulse","revision":"`+revision+`"}`)))
	if response.Code != http.StatusOK || transport.snapshot().PendingSlots["bass"] != "pulse" {
		t.Fatalf("slot command: %d %s", response.Code, response.Body.String())
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Kind != "slot" || event.Bar != 3 || event.Track != "bass" || event.Name != "pulse" {
			t.Fatalf("wrong slot event: %+v", event)
		}
		transport.markLanded(event)
	default:
		t.Fatal("slot did not land")
	}
	if len(transport.snapshot().PendingSlots) != 0 {
		t.Fatal("landed slot remained pending")
	}
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"slot","track":"drums","pattern":"beat","revision":"`+revision+`"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("second-track slot: %d %s", response.Code, response.Body.String())
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Kind != "slot" || event.Bar != 4 || event.Track != "drums" || event.Name != "beat" {
			t.Fatalf("wrong second-track event: %+v", event)
		}
	default:
		t.Fatal("second-track slot did not land")
	}
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"slot","track":"bass","pattern":"missing","revision":"`+revision+`"}`)))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown slot: %d", response.Code)
	}
	transport.playing = false
	response = httptest.NewRecorder()
	studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"launch","scene":"main","revision":"`+revision+`"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("stopped transport launch: %d", response.Code)
	}
}

func TestStudioQueuesNewlySavedSlotAfterScoreOffer(t *testing.T) {
	_, path := studioTestHandler(t)
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.playing, transport.last = stream, true, fingerprint
	p, err := compileStudioSource(path, []byte(studioScore))
	if err != nil {
		t.Fatal(err)
	}
	studio := &studio{path: path, lastGoodSource: []byte(studioScore), lastGoodProject: p, transport: transport}
	updated := strings.ReplaceAll(studioScore, "pulse", "riff")
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(`{"action":"slot","track":"bass","pattern":"riff","revision":"`+studioRevision([]byte(updated))+`"}`))
	response := httptest.NewRecorder()
	studio.transportCommand(response, request)
	if response.Code != http.StatusOK || !transport.snapshot().Pending || transport.snapshot().PendingSlots["bass"] != "riff" {
		t.Fatalf("new score/slot queue: %d %s", response.Code, response.Body.String())
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	if event := <-stream.Events(); event.Kind != "edit" || event.Bar != 2 {
		t.Fatalf("edit did not precede slot: %+v", event)
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8); err != nil {
		t.Fatal(err)
	}
	if event := <-stream.Events(); event.Kind != "slot" || event.Bar != 3 || event.Name != "riff" {
		t.Fatalf("newly saved slot did not launch: %+v", event)
	}
}
