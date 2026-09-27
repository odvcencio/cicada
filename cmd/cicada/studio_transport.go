package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/ebitengine/oto/v3"
	"m31labs.dev/cicada/host/liveplay"
)

type studioTransport struct {
	path         string
	mu           sync.Mutex
	pollMu       sync.Mutex
	stream       *liveplay.Player
	player       *oto.Player
	device       *oto.Context
	cancel       context.CancelFunc
	last         [32]byte
	playing      bool
	pending      bool
	pendingScene string
	pendingSlots map[string]string
	scene        string
	landed       int64
	errText      string
	history      *studioHistory
}

type transportSnapshot struct {
	Type         string            `json:"type"`
	Playing      bool              `json:"playing"`
	Bar          int64             `json:"bar"`
	Step         int64             `json:"step"`
	Pending      bool              `json:"pending"`
	PendingScene string            `json:"pendingScene,omitempty"`
	PendingSlots map[string]string `json:"pendingSlots,omitempty"`
	Scene        string            `json:"scene,omitempty"`
	Landed       int64             `json:"landedBar,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func newStudioTransport(path string) *studioTransport { return &studioTransport{path: path} }

func (t *studioTransport) snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := transportSnapshot{Type: "cicada/transport", Playing: t.playing, Bar: 1, Step: 1, Pending: t.pending, PendingScene: t.pendingScene, Scene: t.scene, Landed: t.landed, Error: t.errText}
	if len(t.pendingSlots) > 0 {
		state.PendingSlots = make(map[string]string, len(t.pendingSlots))
		for track, pattern := range t.pendingSlots {
			state.PendingSlots[track] = pattern
		}
	}
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
	if t.stream != nil {
		t.stream.CancelScene()
		t.stream.CancelPatterns()
	}
	t.playing = false
	t.pendingScene = ""
	clear(t.pendingSlots)
}

func (t *studioTransport) launchScene(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a scene")
	}
	if err := t.stream.LaunchScene(name); err != nil {
		return err
	}
	t.pendingScene, t.errText = name, ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("Scene %s queued for the next bar", name), position.Bar, position.Step, "")
	}
	return nil
}

func (t *studioTransport) launchPattern(track, pattern string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a pattern")
	}
	if err := t.stream.SelectPattern(track, pattern); err != nil {
		return err
	}
	if t.pendingSlots == nil {
		t.pendingSlots = make(map[string]string)
	}
	t.pendingSlots[track], t.errText = pattern, ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("%s queued on %s for the next bar", pattern, track), position.Bar, position.Step, "")
	}
	return nil
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
			t.markLanded(event)
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

func (t *studioTransport) markLanded(event liveplay.Event) {
	t.mu.Lock()
	switch event.Kind {
	case "scene":
		t.scene, t.landed = event.Name, event.Bar
		if t.pendingScene == event.Name {
			t.pendingScene = ""
		}
	case "scene-error":
		if t.pendingScene == event.Name {
			t.pendingScene = ""
		}
		t.errText = fmt.Sprintf("scene %q is no longer in the playing score", event.Name)
	case "slot":
		t.landed = event.Bar
		if t.pendingSlots[event.Track] == event.Name {
			delete(t.pendingSlots, event.Track)
		}
	case "slot-error":
		if t.pendingSlots[event.Track] == event.Name {
			delete(t.pendingSlots, event.Track)
		}
		t.errText = fmt.Sprintf("pattern %q is no longer on track %q in the playing score", event.Name, event.Track)
	default:
		t.pending, t.landed = false, event.Bar
	}
	t.mu.Unlock()
	if t.history != nil {
		if event.Kind == "slot" {
			t.history.record("landed", fmt.Sprintf("%s launched on %s at bar %d", event.Name, event.Track, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "slot-error" {
			t.history.record("error", fmt.Sprintf("%s was not on %s in the playing score", event.Name, event.Track), event.Bar, 1, "")
		} else if event.Kind == "scene" {
			t.history.record("landed", fmt.Sprintf("Scene %s launched at bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "scene-error" {
			t.history.record("error", fmt.Sprintf("Scene %s was not in the playing score", event.Name), event.Bar, 1, "")
		} else {
			t.history.record("landed", fmt.Sprintf("Edit landed at bar %d", event.Bar), event.Bar, 1, "")
		}
	}
}

func (t *studioTransport) poll() {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
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
	position := t.stream.Position()
	t.mu.Unlock()
	if t.history != nil {
		t.history.record("queued", "Edit queued for the next bar", position.Bar, position.Step, "")
	}
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
		Action   string `json:"action"`
		Scene    string `json:"scene"`
		Revision string `json:"revision"`
		Track    string `json:"track"`
		Pattern  string `json:"pattern"`
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
	case "launch", "slot":
		s.mu.Lock()
		current, err := os.ReadFile(s.path)
		if err == nil {
			if p, parseErr := compileStudioSource(s.path, current); parseErr == nil {
				s.lastGoodSource, s.lastGoodProject = current, p
			}
		}
		if err != nil || s.lastGoodProject == nil {
			s.mu.Unlock()
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "score is unavailable"})
			return
		}
		if input.Revision == "" || input.Revision != studioRevision(s.lastGoodSource) {
			s.mu.Unlock()
			studioJSON(w, http.StatusConflict, map[string]string{"error": "score changed on disk; reload before launching a scene"})
			return
		}
		project := s.lastGoodProject
		s.mu.Unlock()
		if s.transport.snapshot().Playing {
			s.transport.poll()
		}
		var launchErr error
		if input.Action == "launch" {
			found := false
			for _, scene := range project.Scenes {
				if scene.ID == input.Scene {
					found = true
					break
				}
			}
			if !found {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("scene %q is not in the score", input.Scene)})
				return
			}
			launchErr = s.transport.launchScene(input.Scene)
		} else {
			found := false
			for _, track := range project.Tracks {
				if track.ID != input.Track {
					continue
				}
				for _, slot := range track.Slots {
					if slot != nil && *slot == input.Pattern {
						found = true
						break
					}
				}
				break
			}
			if !found {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("pattern %q is not on track %q", input.Pattern, input.Track)})
				return
			}
			launchErr = s.transport.launchPattern(input.Track, input.Pattern)
		}
		if launchErr != nil {
			studioJSON(w, http.StatusConflict, map[string]string{"error": launchErr.Error()})
			return
		}
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be play, stop, launch, or slot"})
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
