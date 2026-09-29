package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/kernel/cmd"
)

type studioTransport struct {
	path          string
	audioBackend  string
	mu            sync.Mutex
	pollMu        sync.Mutex
	stream        *liveplay.Player
	audio         studioAudioDevice
	audioOptions  studioAudioOptions
	sampleRate    int
	cancel        context.CancelFunc
	last          [32]byte
	playing       bool
	pending       bool
	pendingSong   string
	pendingSongID uint64
	snapshotSeq   uint64
	scene         string
	stoppedTracks map[string]bool
	activeSlots   map[string]string
	landed        int64
	errText       string
	history       *studioHistory
	audioNull     bool
	latestMeter   atomic.Pointer[studioMeterSnapshot]
	meterSequence atomic.Uint64
}

type studioMeterSnapshot struct {
	sequence uint64
	frame    liveplay.MeterFrame
	loudness liveplay.LoudnessSnapshot
}

type transportSnapshot struct {
	Type                string            `json:"type"`
	Sequence            uint64            `json:"sequence"`
	Playing             bool              `json:"playing"`
	Bar                 int64             `json:"bar"`
	Step                int64             `json:"step"`
	Pending             bool              `json:"pending"`
	PendingScene        string            `json:"pendingScene,omitempty"`
	PendingSong         string            `json:"pendingSong,omitempty"`
	PendingSlots        map[string]string `json:"pendingSlots,omitempty"`
	PendingSlotQuantize map[string]uint32 `json:"pendingSlotQuantize,omitempty"`
	PendingQuantize     uint32            `json:"pendingQuantize,omitempty"`
	StoppedTracks       []string          `json:"stoppedTracks,omitempty"`
	ActiveSlots         map[string]string `json:"activeSlots,omitempty"`
	Scene               string            `json:"scene,omitempty"`
	Landed              int64             `json:"landedBar,omitempty"`
	Error               string            `json:"error,omitempty"`
}

func newStudioTransport(path string) *studioTransport {
	return &studioTransport{path: path, audioBackend: string(audiobackend.DefaultFor("studio")), audioOptions: defaultStudioAudioOptions()}
}

func (t *studioTransport) selectedAudioBackend() string {
	if t.audioNull {
		return string(audiobackend.Null)
	}
	if t.audioBackend != "" {
		return t.audioBackend
	}
	return string(audiobackend.DefaultFor("studio"))
}

func validStudioQuantize(value cmd.Quantize) bool {
	switch value {
	case 1, 2, 5, 6, 8:
		return true
	default:
		return false
	}
}

func (t *studioTransport) snapshot() transportSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshotSeq++
	// Event delivery is bounded; recover scene truth even if a UI event was dropped.
	if t.stream != nil {
		if scene := t.stream.CurrentScene(); scene != "" && scene != t.scene {
			t.scene, t.activeSlots = scene, nil
		}
	}
	state := transportSnapshot{Type: "cicada/transport", Sequence: t.snapshotSeq, Playing: t.playing, Bar: 1, Step: 1, Pending: t.pending, Scene: t.scene, Landed: t.landed, Error: t.errText}
	if len(t.activeSlots) != 0 {
		state.ActiveSlots = make(map[string]string, len(t.activeSlots))
		for track, pattern := range t.activeSlots {
			state.ActiveSlots[track] = pattern
		}
	}
	if t.stream != nil {
		if id := t.stream.PendingSongEntryID(); id != 0 && id == t.pendingSongID {
			state.PendingSong = t.pendingSong
		}
		state.PendingScene = t.stream.PendingScene()
		state.PendingQuantize = t.stream.PendingSceneQuantize()
		for _, request := range t.stream.PendingPatterns() {
			if request.Track != "" {
				if state.PendingSlots == nil {
					state.PendingSlots = make(map[string]string)
				}
				state.PendingSlots[request.Track] = request.Pattern
			}
		}
		state.PendingSlotQuantize = t.stream.PendingPatternQuantizes()
		position := t.stream.Position()
		state.Bar, state.Step = position.Bar, position.Step
	}
	for track, stopped := range t.stoppedTracks {
		if stopped {
			state.StoppedTracks = append(state.StoppedTracks, track)
		}
	}
	return state
}

func (t *studioTransport) start() error { return t.startFrom(-1, "", nil, [32]byte{}) }

func (t *studioTransport) startFrom(index int, scene string, prepared *liveplay.Score, preparedHash [32]byte) error {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	sampleRate, err := t.ensureSampleRate()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stream != nil {
		audioFailed := false
		if t.audio != nil {
			if err := t.audio.Err(); err != nil {
				t.audio.Pause()
				_ = t.audio.Close()
				t.audio = nil
				audioFailed = true
				t.playing, t.errText = false, err.Error()
			}
		}
		if index >= 0 {
			if prepared != nil && preparedHash != t.last {
				if err := t.stream.Offer(*prepared); err != nil {
					return err
				}
				t.last, t.pending = preparedHash, true
			}
			requestID, err := t.stream.QueueSongEntry(index, scene)
			if err != nil {
				return err
			}
			t.pendingSong, t.pendingSongID = scene, requestID
		}
		if !t.playing {
			t.stream.ResetLoudness()
		}
		if t.audio == nil && (!t.playing || audioFailed) {
			audio, openErr := openStudioAudio(t.stream, t.audioOptions, t.selectedAudioBackend(), sampleRate)
			if openErr != nil {
				t.playing, t.errText = false, openErr.Error()
				return openErr
			}
			audio.SetMonitor(studioMonitorOptions(t.audioOptions))
			t.audio = audio
		}
		if t.audio != nil {
			if err := t.audio.Play(); err != nil {
				t.audio.Pause()
				_ = t.audio.Close()
				t.audio = nil
				t.playing, t.errText = false, err.Error()
				return err
			}
		}
		t.playing, t.errText = true, ""
		return nil
	}
	var fingerprint [32]byte
	var initial liveplay.Score
	if prepared != nil {
		fingerprint, initial = preparedHash, *prepared
		if initial.SampleRate != sampleRate {
			return fmt.Errorf("prepared score is %d Hz but the selected audio endpoints use %d Hz", initial.SampleRate, sampleRate)
		}
	} else {
		var err error
		fingerprint, err = playSourceHash(t.path)
		if err != nil {
			return err
		}
		initial, err = compileLiveScoreAtRate(t.path, sampleRate)
		if err != nil {
			return err
		}
	}
	stream, err := liveplay.New(initial, sampleRate)
	if err != nil {
		return err
	}
	var requestID uint64
	if index >= 0 {
		requestID, err = stream.QueueSongEntry(index, scene)
		if err != nil {
			return err
		}
	}
	var audio studioAudioDevice
	audio, err = openStudioAudio(stream, t.audioOptions, t.selectedAudioBackend(), sampleRate)
	if err != nil {
		stream.Close()
		return err
	}
	audio.SetMonitor(studioMonitorOptions(t.audioOptions))
	if audio != nil {
		if err := audio.Play(); err != nil {
			audio.Pause()
			_ = audio.Close()
			stream.Close()
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.stream, t.audio, t.sampleRate, t.last, t.cancel = stream, audio, sampleRate, fingerprint, cancel
	t.playing, t.errText = true, ""
	if index >= 0 {
		t.pendingSong, t.pendingSongID = scene, requestID
	} else {
		t.scene = stream.CurrentScene()
	}
	go t.watch(ctx, stream)
	return nil
}

// pause halts playback and keeps the stream, so play resumes at the same
// position (D18: Space toggles play and pause).
func (t *studioTransport) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pauseLocked()
}

func (t *studioTransport) pauseLocked() {
	if t.audio != nil {
		audio := t.audio
		audio.Pause()
		// Release duplex hosts after Pause so they no longer hold the input endpoint.
		if audio.StopClosesDevice() {
			if err := audio.Close(); err != nil {
				t.errText = err.Error()
			}
			t.audio = nil
		}
	}
	if t.stream != nil {
		t.stream.CancelScene()
		t.stream.CancelPatterns()
		t.stream.CancelStart()
	}
	t.playing = false
	t.pendingSong = ""
	t.pendingSongID = 0
}

// stop halts playback and returns to bar 1 by discarding the stream (D18).
// The next start builds a new stream from the beginning.
func (t *studioTransport) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pauseLocked()
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.audio != nil {
		if err := t.audio.Close(); err != nil {
			t.errText = err.Error()
		}
		t.audio = nil
	}
	if t.stream != nil {
		t.stream.Close()
		t.stream = nil
	}
	// A discarded stream has no muted tracks; do not report them as stopped.
	t.stoppedTracks = nil
}

// returnToStart moves to bar 1. A playing transport keeps playing from the
// start; a stopped one stays stopped (D18).
func (t *studioTransport) returnToStart() error {
	t.mu.Lock()
	wasPlaying := t.playing
	t.mu.Unlock()
	t.stop()
	if wasPlaying {
		return t.start()
	}
	return nil
}

func (t *studioTransport) launchScene(name string, quantizes ...cmd.Quantize) error {
	quantize := cmd.Quantize(2)
	if len(quantizes) != 0 {
		quantize = quantizes[0]
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a scene")
	}
	if err := t.stream.LaunchSceneQuantized(name, quantize); err != nil {
		return err
	}
	t.errText = ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("Scene %s queued for the next bar", name), position.Bar, position.Step, "")
	}
	return nil
}

func (t *studioTransport) launchPattern(track, pattern string, quantizes ...cmd.Quantize) error {
	quantize := cmd.Quantize(2)
	if len(quantizes) != 0 {
		quantize = quantizes[0]
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before launching a pattern")
	}
	if err := t.stream.SelectPatternQuantized(track, pattern, quantize); err != nil {
		return err
	}
	t.errText = ""
	if t.history != nil {
		position := t.stream.Position()
		t.history.record("queued", fmt.Sprintf("%s queued on %s for the next bar", pattern, track), position.Bar, position.Step, "")
	}
	return nil
}

func (t *studioTransport) stopTrack(track string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.playing || t.stream == nil {
		return fmt.Errorf("press Play before stopping a track")
	}
	index, ok := t.stream.TrackIndex(track)
	if !ok {
		return fmt.Errorf("unknown track %q", track)
	}
	if err := t.stream.SetMute(index, true); err != nil {
		return err
	}
	if t.stoppedTracks == nil {
		t.stoppedTracks = make(map[string]bool)
	}
	t.stoppedTracks[track] = true
	return nil
}

func (t *studioTransport) resumeStoppedTracks(only string) {
	for track := range t.stoppedTracks {
		if only != "" && track != only {
			continue
		}
		if index, ok := t.stream.TrackIndex(track); ok {
			_ = t.stream.SetMute(index, false)
		}
		delete(t.stoppedTracks, track)
	}
}

func (t *studioTransport) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	if t.audio != nil {
		t.audio.Pause()
		_ = t.audio.Close()
		t.audio = nil
	}
	if t.stream != nil {
		t.stream.Close()
	}
	t.playing = false
}

func (t *studioTransport) resetLoudness() {
	t.mu.Lock()
	stream := t.stream
	t.mu.Unlock()
	if stream != nil {
		stream.ResetLoudness()
	}
}

func (t *studioTransport) loudness() liveplay.LoudnessSnapshot {
	t.mu.Lock()
	stream := t.stream
	t.mu.Unlock()
	if stream != nil {
		return stream.Loudness()
	}
	return liveplay.LoudnessSnapshot{}
}

func (t *studioTransport) watch(ctx context.Context, stream *liveplay.Player) {
	ticker := time.NewTicker(100 * time.Millisecond)
	health := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-stream.Events():
			t.markLanded(event)
		case frame := <-stream.Meters():
			t.publishMeter(frame, stream.Loudness())
		case <-ticker.C:
			t.poll()
		case <-health.C:
			t.mu.Lock()
			if t.audio != nil {
				if err := t.audio.Err(); err != nil {
					t.audio.Pause()
					t.errText, t.playing = err.Error(), false
				}
			}
			t.mu.Unlock()
		}
	}
}

func (t *studioTransport) publishMeter(frame liveplay.MeterFrame, loudnessSnapshots ...liveplay.LoudnessSnapshot) {
	sequence := t.meterSequence.Add(1)
	snapshot := studioMeterSnapshot{sequence: sequence, frame: frame}
	if len(loudnessSnapshots) != 0 {
		snapshot.loudness = loudnessSnapshots[0]
	} else {
		snapshot.loudness = t.loudness()
	}
	t.latestMeter.Store(&snapshot)
}

func (t *studioTransport) markLanded(event liveplay.Event) {
	t.mu.Lock()
	if event.Kind == "note-on" || event.Kind == "note-off" {
		t.mu.Unlock()
		return
	}
	switch event.Kind {
	case "scene":
		t.resumeStoppedTracks("")
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "song-scene":
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "scene-error":
		t.errText = fmt.Sprintf("scene %q is no longer in the playing score", event.Name)
	case "song":
		t.scene, t.landed = event.Name, event.Bar
		t.activeSlots = nil
	case "song-error":
		t.errText = fmt.Sprintf("song block %q is no longer in the playing score", event.Name)
	case "slot":
		t.resumeStoppedTracks(event.Track)
		t.landed = event.Bar
		if t.activeSlots == nil {
			t.activeSlots = make(map[string]string)
		}
		t.activeSlots[event.Track] = event.Name
	case "slot-error":
		t.errText = fmt.Sprintf("pattern %q is no longer on track %q in the playing score", event.Name, event.Track)
	case "note-error":
		t.errText = event.Name
	default:
		t.pending, t.landed = false, event.Bar
	}
	t.mu.Unlock()
	if t.history != nil {
		if event.Kind == "song" {
			t.history.record("landed", fmt.Sprintf("Song started at %s, bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "song-scene" {
			t.history.record("landed", fmt.Sprintf("Song advanced to %s at bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "song-error" {
			t.history.record("error", fmt.Sprintf("Song block %s was not in the playing score", event.Name), event.Bar, 1, "")
		} else if event.Kind == "slot" {
			t.history.record("landed", fmt.Sprintf("%s launched on %s at bar %d", event.Name, event.Track, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "slot-error" {
			t.history.record("error", fmt.Sprintf("%s was not on %s in the playing score", event.Name, event.Track), event.Bar, 1, "")
		} else if event.Kind == "scene" {
			t.history.record("landed", fmt.Sprintf("Scene %s launched at bar %d", event.Name, event.Bar), event.Bar, 1, "")
		} else if event.Kind == "scene-error" {
			t.history.record("error", fmt.Sprintf("Scene %s was not in the playing score", event.Name), event.Bar, 1, "")
		} else if event.Kind == "note-error" {
			t.history.record("error", event.Name, event.Bar, 1, "")
		} else if event.Kind == "note-on" || event.Kind == "note-off" {
			return
		} else {
			t.history.record("landed", fmt.Sprintf("Edit landed at bar %d", event.Bar), event.Bar, 1, "")
		}
	}
}

func (t *studioTransport) poll() {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	source, err := os.ReadFile(t.path)
	if err != nil {
		t.setError(err)
		return
	}
	fingerprint, err := playSourceHashBytes(t.path, source)
	if err != nil {
		t.setError(err)
		return
	}
	t.mu.Lock()
	if fingerprint == t.last {
		t.mu.Unlock()
		return
	}
	stream := t.stream
	sampleRate := t.sampleRate
	t.mu.Unlock()
	if stream == nil {
		return
	}
	if sampleRate <= 0 {
		sampleRate = liveSampleRate
	}
	project, err := compileStudioSource(t.path, source)
	var next liveplay.Score
	if err == nil {
		next, err = compileLiveProjectAtRate(t.path, project, sampleRate)
	}
	if err == nil {
		err = stream.Offer(next)
	}
	if err != nil {
		t.setError(err)
		return
	}
	t.mu.Lock()
	t.last = fingerprint
	// Clear the transport error when a valid revision lands (UX review finding 2).
	t.pending, t.errText = true, ""
	position := stream.Position()
	t.mu.Unlock()
	if t.history != nil && !t.history.consumeRecordedTake(studioRevision(source)) {
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
		Entry    int    `json:"entry"`
		Quantize uint32 `json:"quantize"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input); err != nil {
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	quantize := cmd.Quantize(input.Quantize)
	if input.Action == "launch" || input.Action == "slot" {
		if input.Quantize == 0 {
			quantize = cmd.Quantize(2)
		}
		if !validStudioQuantize(quantize) {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "quantize must be next beat, next bar, 2 bars, or 4 bars"})
			return
		}
	}
	switch input.Action {
	case "play":
		if err := s.transport.start(); err != nil {
			s.transport.setError(err)
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "pause":
		s.transport.pause()
	case "stop":
		s.transport.stop()
	case "home":
		if err := s.transport.returnToStart(); err != nil {
			s.transport.setError(err)
			studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
	case "trackStop":
		if err := s.transport.stopTrack(input.Track); err != nil {
			studioJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	case "launch", "slot", "playFrom":
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
		s.transport.mu.Lock()
		hasStream := s.transport.stream != nil
		s.transport.mu.Unlock()
		if hasStream && input.Action != "playFrom" {
			s.transport.poll()
		}
		if input.Action == "playFrom" {
			latest, readErr := os.ReadFile(s.path)
			if readErr != nil || studioRevision(latest) != input.Revision {
				studioJSON(w, http.StatusConflict, map[string]string{"error": "score changed on disk; reload before starting the song"})
				return
			}
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
			launchErr = s.transport.launchScene(input.Scene, quantize)
		} else if input.Action == "slot" {
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
			launchErr = s.transport.launchPattern(input.Track, input.Pattern, quantize)
		} else {
			if input.Entry < 0 || input.Entry >= len(project.Song) || project.Song[input.Entry].Scene != input.Scene {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "song block is no longer in this position"})
				return
			}
			sampleRate, rateErr := s.transport.ensureSampleRate()
			if rateErr != nil {
				s.transport.setError(rateErr)
				studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": rateErr.Error()})
				return
			}
			prepared, compileErr := compileLiveProjectAtRate(s.path, project, sampleRate)
			if compileErr != nil {
				studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": compileErr.Error()})
				return
			}
			fingerprint, hashErr := playSourceHashBytes(s.path, current)
			if hashErr != nil {
				studioJSON(w, http.StatusServiceUnavailable, map[string]string{"error": hashErr.Error()})
				return
			}
			launchErr = s.transport.startFrom(input.Entry, input.Scene, &prepared, fingerprint)
		}
		if launchErr != nil {
			s.transport.setError(launchErr)
			studioJSON(w, http.StatusConflict, map[string]string{"error": launchErr.Error()})
			return
		}
	default:
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be play, stop, trackStop, launch, slot, or playFrom"})
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
	// Send transport snapshot immediately when the WebSocket connects so stopped transports show their real position.
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	err = wsjson.Write(writeCtx, connection, s.transport.snapshot())
	cancel()
	if err != nil {
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		writeCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := wsjson.Write(writeCtx, connection, s.transport.snapshot())
		cancel()
		if err != nil {
			return
		}
	}
}
