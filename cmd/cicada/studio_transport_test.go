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
	updated := strings.Replace(studioScore, "Studio", "Next Studio", 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	transport.poll()
	if !transport.snapshot().Pending {
		t.Fatal("validated file edit was not queued")
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Bar != 2 || event.Name != path {
			t.Fatalf("wrong edit landing: %+v", event)
		}
	default:
		t.Fatal("edit did not land at next bar")
	}
	if position := stream.Position(); position.Bar != 2 {
		t.Fatalf("native transport position: %+v", position)
	}
}
