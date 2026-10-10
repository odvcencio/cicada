package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
)

func TestLiveSocketSendsLatestFramesThenStreamsTransport(t *testing.T) {
	handler, _, s := studioTestStudio(t)
	s.transport.audioNull = true
	if err := s.transport.start(); err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	seen := map[liveplay.FrameKind]bool{}
	for len(seen) < 3 {
		kind, data, err := conn.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || len(data) < 2 || data[0] != liveplay.FrameVersion {
			t.Fatalf("frame: %v %d %v", kind, len(data), err)
		}
		seen[liveplay.FrameKind(data[1])] = true
	}
	if !seen[liveplay.FrameHealth] {
		t.Fatal("health frame was not published at start")
	}
}

func TestLiveSocketRejectsRequestsWithoutTheServiceToken(t *testing.T) {
	handler, _, _ := studioTestStudio(t)
	private, token, err := privateStudioService(handler)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(private)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/live"
	conn, response, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("dial without token succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("response without token: %v %v", response, err)
	}
	conn, _, err = websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	conn.CloseNow()
}

func TestLiveSocketPublishesHealthAtStartAndTwiceASecond(t *testing.T) {
	_, _, s := studioTestStudio(t)
	s.transport.audioNull = true
	sub := s.transport.telemetry.Subscribe(256)
	defer s.transport.telemetry.Unsubscribe(sub)
	if err := s.transport.start(); err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	if s.transport.telemetry.Latest()[liveplay.FrameHealth] == nil {
		t.Fatal("no health frame right after start")
	}
	time.Sleep(1100 * time.Millisecond)
	health := 0
	for len(sub.C) > 0 {
		if <-sub.C == liveplay.FrameHealth {
			health++
		}
	}
	if health < 3 || health > 4 { // one at start plus about two in 1.1 s
		t.Fatalf("health frames in 1.1 s: %d", health)
	}
}

func TestLiveSocketPreviewSetAndEndRestoreTheCommittedValue(t *testing.T) {
	handler, path, s := studioTestStudio(t)
	// Bass is deliberately second: the client must resolve its entity ID.
	source := strings.Replace(studioScore, "track bass acid {}\ntrack drums drums {}", "track drums drums {}\ntrack bass acid { level = -6dB }", 1)
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s.transport.audioNull = true
	if err := s.transport.start(); err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	s.transport.mu.Lock()
	stream := s.transport.stream
	s.transport.mu.Unlock()
	track, ok := stream.TrackIndex("bass")
	if !ok || track != 1 {
		t.Fatalf("resolved bass: %d, %v", track, ok)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	write := func(message string) {
		t.Helper()
		if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`)
	time.Sleep(60 * time.Millisecond)
	if value, ok := stream.OverrideValue(track, kernel.ParamMixGain); !ok || value != 0 {
		t.Fatalf("preview override: %g, %v", value, ok)
	}
	if _, ok := stream.OverrideValue(0, kernel.ParamMixGain); ok {
		t.Fatal("preview changed drums instead of bass")
	}
	write(`{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":true}`)
	// An error response is a barrier for earlier text messages on this socket.
	write(`{"type":"preview-set","entity":"track:missing","param":"mix.gain","value":0}`)
	readLiveSocketError(t, ctx, conn)
	if value, ok := stream.OverrideValue(track, kernel.ParamMixGain); !ok || value != 0 {
		t.Fatalf("commit end cleared the preview before activation: %g, %v", value, ok)
	}
	write(`{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":false}`)
	time.Sleep(60 * time.Millisecond)
	if value, ok := stream.OverrideValue(track, kernel.ParamMixGain); !ok || value != -6 {
		t.Fatalf("cancel did not restore the committed value: %g, %v", value, ok)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != source {
		t.Fatalf("preview changed committed source: %v", err)
	}
}

func readLiveSocketError(t *testing.T, ctx context.Context, conn *websocket.Conn) string {
	t.Helper()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.MessageBinary {
			continue
		}
		var response struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &response); err != nil || response.Type != "error" || response.Message == "" {
			t.Fatalf("error response: %s, %v", data, err)
		}
		return response.Message
	}
}

func TestLiveSocketPreviewRejectsInvalidMessages(t *testing.T) {
	handler, _, s := studioTestStudio(t)
	s.transport.audioNull = true
	if err := s.transport.start(); err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for _, message := range []string{
		`{`,
		`{"type":"unknown"}`,
		`{"type":"preview-set","entity":"track:missing","param":"mix.gain","value":0}`,
		`{"type":"preview-set","entity":"0","param":"mix.gain","value":0}`,
		`{"type":"preview-set","entity":"track:bass","param":"missing","value":0}`,
		`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":999}`,
		`{"type":"preview-set","entity":"track:bass","param":"mix.gain"}`,
	} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
			t.Fatal(err)
		}
		readLiveSocketError(t, ctx, conn)
	}
}

func TestLiveSocketPreviewReportsUnavailablePlayer(t *testing.T) {
	handler, _, _ := studioTestStudio(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`)); err != nil {
		t.Fatal(err)
	}
	readLiveSocketError(t, ctx, conn)
}
