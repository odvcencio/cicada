package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"m31labs.dev/cicada/host/liveplay"
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
