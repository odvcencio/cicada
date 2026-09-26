package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/ebitengine/oto/v3"
	"m31labs.dev/cicada/host/liveplay"
)

type studioTransport struct {
	path    string
	mu      sync.Mutex
	stream  *liveplay.Player
	player  *oto.Player
	device  *oto.Context
	cancel  context.CancelFunc
	last    [32]byte
	playing bool
	pending bool
	landed  int64
	errText string
}

type transportSnapshot struct {
	Type    string `json:"type"`
	Playing bool   `json:"playing"`
	Bar     int64  `json:"bar"`
	Step    int64  `json:"step"`
	Pending bool   `json:"pending"`
	Landed  int64  `json:"landedBar,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newStudioTransport(path string) *studioTransport { return &studioTransport{path: path} }

func (t *studioTransport) snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := transportSnapshot{Type: "cicada/transport", Playing: t.playing, Bar: 1, Step: 1, Pending: t.pending, Landed: t.landed, Error: t.errText}
	if t.stream != nil {
		position := t.stream.Position()
		state.Bar, state.Step = position.Bar, position.Step
	}
	return state
}

func (t *studioTransport) start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.player != nil {
		if err := t.device.Err(); err != nil {
			return err
		}
		if err := t.player.Err(); err != nil {
			return err
		}
		t.player.Play()
		t.playing, t.errText = true, ""
		return nil
	}
	fingerprint, err := playSourceHash(t.path)
	if err != nil {
		return err
	}
	initial, err := compileLiveScore(t.path)
	if err != nil {
		return err
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		return err
	}
	device, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: liveSampleRate, ChannelCount: 2, Format: oto.FormatFloat32LE,
		BufferSize: 20 * time.Millisecond, ApplicationName: "Cicada Studio",
	})
	if err != nil {
		return err
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("audio device did not become ready within 10 seconds")
	}
	if err := device.Err(); err != nil {
		return err
	}
	player := device.NewPlayer(stream)
	player.SetBufferSize(liveBlockFrames * 8 * 4)
	ctx, cancel := context.WithCancel(context.Background())
	t.stream, t.device, t.player, t.last, t.cancel = stream, device, player, fingerprint, cancel
	t.playing, t.errText = true, ""
	player.Play()
	go t.watch(ctx)
	return nil
}

func (t *studioTransport) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.player != nil {
		t.player.PauseAndStopReading()
	}
	t.playing = false
}

func (t *studioTransport) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	if t.player != nil {
		t.player.PauseAndStopReading()
		_ = t.player.Close()
	}
	t.playing = false
}

func (t *studioTransport) watch(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	health := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-t.stream.Events():
			t.mu.Lock()
			t.pending, t.landed = false, event.Bar
			t.mu.Unlock()
		case <-ticker.C:
			t.poll()
		case <-health.C:
			t.mu.Lock()
			if err := t.device.Err(); err != nil {
				t.errText, t.playing = err.Error(), false
			} else if err := t.player.Err(); err != nil {
				t.errText, t.playing = err.Error(), false
			}
			t.mu.Unlock()
		}
	}
}

func (t *studioTransport) poll() {
	fingerprint, err := playSourceHash(t.path)
	if err != nil {
		t.setError(err)
		return
	}
	t.mu.Lock()
	if fingerprint == t.last {
		t.mu.Unlock()
		return
	}
	t.last = fingerprint
	t.mu.Unlock()
	next, err := compileLiveScore(t.path)
	if err == nil {
		err = t.stream.Offer(next)
	}
	if err != nil {
		t.setError(err)
		return
	}
	t.mu.Lock()
	t.pending, t.errText = true, ""
	t.mu.Unlock()
}

func (t *studioTransport) setError(err error) {
	t.mu.Lock()
	t.errText = err.Error()
	t.mu.Unlock()
}

func studioSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == r.Host
}

func (s *studio) transportCommand(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin transport commands are not allowed"})
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	switch input.Action {
	case "play":
		if err := s.transport.start(); err != nil {
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "stop":
		s.transport.stop()
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be play or stop"})
		return
	}
	studioJSON(w, http.StatusOK, s.transport.snapshot())
}

func (s *studio) transportState(w http.ResponseWriter, _ *http.Request) {
	studioJSON(w, http.StatusOK, s.transport.snapshot())
}

func (s *studio) transportSocket(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		http.Error(w, "cross-origin transport stream is not allowed", http.StatusForbidden)
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	ctx := connection.CloseRead(context.Background())
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		writeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := wsjson.Write(writeCtx, connection, s.transport.snapshot())
		cancel()
		if err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
