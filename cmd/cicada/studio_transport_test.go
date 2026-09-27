package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
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

func TestStudioParamsEndpointAndAudioScript(t *testing.T) {
	handler, _ := studioTestHandler(t)
	response := studioCall(t, handler, "/api/params", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("params endpoint: %d %s", response.Code, response.Body.String())
	}
	var params struct {
		Revision  string           `json:"revision"`
		Registry  []map[string]any `json:"registry"`
		Addresses []map[string]any `json:"addresses"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &params); err != nil {
		t.Fatal(err)
	}
	if params.Revision != studioRevision([]byte(studioScore)) || len(params.Registry) != len(kernel.Params) || len(params.Addresses) == 0 {
		t.Fatalf("incomplete current registry response: rev=%q registry=%d addresses=%d", params.Revision, len(params.Registry), len(params.Addresses))
	}
	path := filepath.Join(t.TempDir(), "paths.cicada")
	source := strings.Replace(studioScore, "track bass acid {}", "fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.3 }", 1)
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pathStudio, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pathStudio.transport.close()
	pathResponse := studioCall(t, pathStudio.routes(), "/api/params", nil)
	var pathParams studioParamsResponse
	if pathResponse.Code != http.StatusOK || json.Unmarshal(pathResponse.Body.Bytes(), &pathParams) != nil {
		t.Fatalf("P1 parameter endpoint: %d %s", pathResponse.Code, pathResponse.Body.String())
	}
	addresses := map[string]bool{}
	for _, address := range pathParams.Addresses {
		addresses[address.Address] = true
	}
	for _, required := range []string{"bass.send.delay", "delay.feedback"} {
		if !addresses[required] {
			t.Errorf("P1 address %q missing from /api/params: %v", required, addresses)
		}
	}
	for old := range addresses {
		if strings.HasPrefix(old, "fx.") || strings.Contains(old, "send_a") || strings.Contains(old, "send_b") {
			t.Errorf("legacy address %q remains in /api/params", old)
		}
	}
	page := studioCall(t, handler, "/", nil)
	if page.Code != http.StatusOK || strings.Count(page.Body.String(), `src="/studio-audio.js"`) != 1 || !strings.Contains(page.Body.String(), `src="/studio-master.js"`) || !strings.Contains(page.Body.String(), `href="#master">Master</a>`) || !strings.Contains(page.Body.String(), `id="master-meter-fill"`) || !strings.Contains(page.Body.String(), `id="loudness-history"`) || !strings.Contains(page.Body.String(), `id="gain-reduction-history"`) || !strings.Contains(page.Body.String(), `id="reset-integrated"`) || !strings.Contains(page.Body.String(), `id="export-wav"`) || !strings.Contains(page.Body.String(), `href="#mixer-settings">Open mixer compressor settings</a>`) || !strings.Contains(page.Body.String(), `data-track-meter="bass"`) || !strings.Contains(page.Body.String(), `data-track-meter="drums"`) || !strings.Contains(page.Body.String(), "score-minus14LUFS.wav") {
		t.Fatalf("Studio meter markup missing: %d", page.Code)
	}
	script := studioCall(t, handler, "/studio-audio.js", nil)
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "window.cicadaAudio") && !strings.Contains(script.Body.String(), "window.cicadaAudio =") {
		t.Fatalf("audio facade not served: %d", script.Code)
	}
	masterScript := studioCall(t, handler, "/studio-master.js", nil)
	if masterScript.Code != http.StatusOK || !strings.Contains(masterScript.Body.String(), "createHistoryBuffer") {
		t.Fatalf("Master view script not served: %d", masterScript.Code)
	}
}

func TestStudioAudioSocketRejectsInvalidParametersWithTypedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "score.cicada")
	audioScore := strings.Replace(studioScore, "track bass acid {}", "fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.1 }", 1)
	if err := os.WriteFile(path, []byte(audioScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.transport.close()
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.stream, s.transport.playing = stream, true
	server := httptest.NewServer(s.routes())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/audio/ws"
	_, response, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"https://outside.example"}}})
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin audio socket: response=%v err=%v", response, err)
	}
	connection, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{server.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for _, test := range []struct {
		message map[string]any
		address string
	}{
		{map[string]any{"type": "param", "address": "unknown.level", "value": -3.5}, "unknown.level"},
		{map[string]any{"type": "param", "address": "bass.level", "value": 7}, "bass.level"},
		{map[string]any{"type": "param", "address": "bass.tune", "value": 440}, "bass.tune"},
	} {
		if err := wsjson.Write(ctx, connection, test.message); err != nil {
			t.Fatal(err)
		}
		var result audioErrorMessage
		if err := wsjson.Read(ctx, connection, &result); err != nil {
			t.Fatal(err)
		}
		if result.Type != "error" || result.Code != "CICADA-PARAM" || result.Address != test.address || result.Message == "" {
			t.Fatalf("typed parameter error: %+v", result)
		}
	}
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "param", "address": "bass.level", "value": -3.5}); err != nil {
		t.Fatal(err)
	}
	for _, param := range []map[string]any{
		{"type": "param", "address": "bass.send.delay", "value": 0.4},
		{"type": "param", "address": "delay.feedback", "value": 0.35},
	} {
		if err := wsjson.Write(ctx, connection, param); err != nil {
			t.Fatal(err)
		}
	}
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "mute", "track": "bass", "on": true}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "solo", "track": "drums", "on": true}); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "loudness-reset"}); err != nil {
		t.Fatal(err)
	}
	s.transport.publishMeter(liveplay.MeterFrame{
		Tick: 1234, TrackCount: 1, TrackIDs: [16]string{"bass"}, Tracks: [16]liveplay.MeterValue{{Peak: .5, RMS: .25}},
		HasReturnA: true, ReturnA: liveplay.MeterValue{Peak: 0, RMS: .1},
		Music: liveplay.MeterValue{Peak: .8, RMS: .4}, MasterPeak: .7, MasterRMS: .3, MasterPre: liveplay.MeterValue{Peak: .8},
		CompGR: 2.5, LimiterGR: .25,
	}, liveplay.LoudnessSnapshot{
		Sequence: 1, MomentaryLUFS: -18.2, ShortTermLUFS: -17.9, IntegratedLUFS: -18.4, RangeLU: 4.1,
		TruePeakDBTP: -1.2, SamplePeakDBFS: -1.5, DroppedBlocks: 3,
		HasMomentary: true, HasShortTerm: true, HasIntegrated: true, HasRange: true, HasTruePeak: true, HasSamplePeak: true,
	})
	var meter struct {
		Type    string                                 `json:"type"`
		Tick    int64                                  `json:"tick"`
		Tracks  map[string]struct{ Peak, RMS float64 } `json:"tracks"`
		Returns map[string]struct{ Peak, RMS float64 } `json:"returns"`
		Buses   map[string]struct{ Peak, RMS float64 } `json:"buses"`
		Master  struct {
			Peak      float64 `json:"peak"`
			RMS       float64 `json:"rms"`
			PrePeak   float64 `json:"pre_peak"`
			CompGR    float64 `json:"comp_gr"`
			LimiterGR float64 `json:"limiter_gr"`
			Over      bool    `json:"over"`
		} `json:"master"`
		Loudness struct {
			Momentary  *float64 `json:"momentary"`
			ShortTerm  *float64 `json:"short_term"`
			Integrated *float64 `json:"integrated"`
			Range      *float64 `json:"range"`
			TruePeak   *float64 `json:"true_peak"`
			SamplePeak *float64 `json:"sample_peak"`
			Dropped    uint64   `json:"dropped_blocks"`
		} `json:"loudness"`
	}
	if err := wsjson.Read(ctx, connection, &meter); err != nil {
		t.Fatal(err)
	}
	if meter.Type != "meters" || meter.Tick != 1234 || math.Abs(meter.Tracks["bass"].Peak+6.0206) > .01 || meter.Returns["a"].Peak != -120 || meter.Master.CompGR != 2.5 || meter.Master.LimiterGR != .25 {
		t.Fatalf("wire meter values: %+v", meter)
	}
	if meter.Loudness.Momentary == nil || *meter.Loudness.Momentary != -18.2 || meter.Loudness.ShortTerm == nil || *meter.Loudness.ShortTerm != -17.9 || meter.Loudness.Integrated == nil || *meter.Loudness.Integrated != -18.4 || meter.Loudness.Range == nil || *meter.Loudness.Range != 4.1 || meter.Loudness.TruePeak == nil || *meter.Loudness.TruePeak != -1.2 || meter.Loudness.SamplePeak == nil || *meter.Loudness.SamplePeak != -1.5 || meter.Loudness.Dropped != 3 {
		t.Fatalf("wire loudness values: %+v", meter.Loudness)
	}
	if _, ok := meter.Returns["b"]; ok {
		t.Fatal("absent return B was included")
	}
	if _, ok := meter.Buses["sfx"]; ok {
		t.Fatal("absent SFX bus was included")
	}
}

func TestStudioNullAudioPacesRenderingWithoutOpeningDevice(t *testing.T) {
	_, path := studioTestHandler(t)
	transport := newStudioTransport(path)
	transport.audioNull = true
	if err := transport.start(); err != nil {
		t.Fatal(err)
	}
	defer transport.close()
	if transport.player != nil || transport.device != nil || transport.stream == nil {
		t.Fatalf("null audio opened a device or missed its stream: player=%v device=%v", transport.player, transport.device)
	}
	time.Sleep(80 * time.Millisecond)
	if got := transport.meterSequence.Load(); got == 0 || got > 8 {
		t.Fatalf("null audio did not render at the expected real-time pace: received %d meter frames in 80 ms", got)
	}
	stream := transport.stream
	transport.stop()
	time.Sleep(80 * time.Millisecond)
	paused := transport.meterSequence.Load()
	time.Sleep(50 * time.Millisecond)
	if got := transport.meterSequence.Load(); got != paused {
		t.Fatalf("null audio kept rendering while stopped: meter frames %d -> %d", paused, got)
	}
	if err := transport.start(); err != nil {
		t.Fatal(err)
	}
	if transport.stream != stream {
		t.Fatal("null audio did not resume the existing stream")
	}
	time.Sleep(80 * time.Millisecond)
	if got := transport.meterSequence.Load(); got <= paused {
		t.Fatal("null audio did not resume rendering")
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

func TestStudioPollKeepsLastCompiledScoreAfterInvalidSave(t *testing.T) {
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
	transport.stream = stream
	transport.last, err = playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	lastGoodHash := transport.last
	if err := os.WriteFile(path, []byte("title \"broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	transport.poll()
	if transport.last != lastGoodHash || transport.snapshot().Error == "" || transport.snapshot().Pending {
		t.Fatalf("invalid save replaced last good score: %+v", transport.snapshot())
	}
	updated := strings.Replace(studioScore, "Studio", "Recovered", 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	transport.poll()
	if transport.last == lastGoodHash || !transport.snapshot().Pending || transport.snapshot().Error != "" {
		t.Fatalf("valid save did not recover: %+v", transport.snapshot())
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

func TestStudioPlayFromSongBlockUsesActiveScore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "song.cicada")
	if err := os.WriteFile(path, []byte(studioSongScore), 0600); err != nil {
		t.Fatal(err)
	}
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
	project, err := compileStudioSource(path, []byte(studioSongScore))
	if err != nil {
		t.Fatal(err)
	}
	transport := newStudioTransport(path)
	transport.stream, transport.playing, transport.last = stream, true, fingerprint
	transport.history = newStudioHistory([]byte(studioSongScore))
	studio := &studio{path: path, lastGoodSource: []byte(studioSongScore), lastGoodProject: project, transport: transport}
	command := func(source, scene string, index int) *httptest.ResponseRecorder {
		t.Helper()
		body := fmt.Sprintf(`{"action":"playFrom","entry":%d,"scene":%q,"revision":%q}`, index, scene, studioRevision([]byte(source)))
		response := httptest.NewRecorder()
		studio.transportCommand(response, httptest.NewRequest(http.MethodPost, "/api/transport", bytes.NewBufferString(body)))
		return response
	}
	if response := command(studioSongScore, "dusk", 2); response.Code != http.StatusOK || transport.snapshot().PendingSong != "dusk" {
		t.Fatalf("song start: %d %s", response.Code, response.Body.String())
	}
	var frame [8]byte
	if _, err := stream.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Kind != "song" || event.Bar != 4 {
			t.Fatalf("wrong song start: %+v", event)
		}
		transport.markLanded(event)
	default:
		t.Fatal("song did not start")
	}
	if state := transport.snapshot(); state.PendingSong != "" || state.Bar != 4 || state.Scene != "dusk" {
		t.Fatalf("song state: %+v", state)
	}
	if response := command(studioSongScore, "chorus", 2); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong song block accepted: %d", response.Code)
	}
	updated := strings.Replace(studioSongScore, "dusk*2", "dusk*5", 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	if response := command(studioSongScore, "dusk", 2); response.Code != http.StatusConflict {
		t.Fatalf("stale song start: %d %s", response.Code, response.Body.String())
	}
	if response := command(updated, "dusk", 2); response.Code != http.StatusOK {
		t.Fatalf("edited song start: %d %s", response.Code, response.Body.String())
	}
	if _, err := stream.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if event := <-stream.Events(); event.Kind != "edit" || event.Bar != 7 {
		t.Fatalf("edit did not lead song jump: %+v", event)
	}
	if event := <-stream.Events(); event.Kind != "song" || event.Bar != 7 {
		t.Fatalf("edited song started at old bar: %+v", event)
	}
	if got := stream.Position(); got.Bar != 7 {
		t.Fatalf("edited song position: %+v", got)
	}
}
