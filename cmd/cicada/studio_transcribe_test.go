package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/host/transcription"
)

func transcriptionUpload(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"tempo": "120", "grid": "16", "key": "C major"} {
		if err := form.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := form.CreateFormFile("wav", "melody.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(data)
	_ = form.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/transcribe", &body)
	r.Host, r.RemoteAddr = "127.0.0.1:1234", "127.0.0.1:4321"
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Origin", "http://127.0.0.1:1234")
	return r
}

func transcriptionWAV() []byte {
	const rate = 16000
	pcm := make([]float32, 2*rate)
	for n, midi := range []int{60, 64, 67, 72} {
		for i := 0; i < rate*4/10; i++ {
			phase := 2 * math.Pi * (440 * math.Exp2(float64(midi-69)/12)) * float64(i) / rate
			envelope := math.Min(1, float64(i)/80) * math.Min(1, float64(rate*4/10-i)/160)
			pcm[n*rate/2+i] = float32(.4 * envelope * (math.Sin(phase) + .2*math.Sin(2*phase)))
		}
	}
	return recording.EncodeWAV(pcm, rate)
}

func TestStudioTranscriptionPreviewsWithoutChangingSource(t *testing.T) {
	handler, path := studioTestHandler(t)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, transcriptionUpload(t, transcriptionWAV()))
	if w.Code != http.StatusOK {
		t.Fatalf("transcription: %d %s", w.Code, w.Body.String())
	}
	var result transcription.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 4 || result.Tempo != 120 || !strings.EqualFold(result.Key, "C major") || !strings.Contains(result.Source, "pattern") {
		t.Fatalf("incomplete preview: notes=%d tempo=%v key=%q", len(result.Notes), result.Tempo, result.Key)
	}
	if source, err := os.ReadFile(path); err != nil || string(source) != studioScore {
		t.Fatal("preview changed source", err)
	}
	revision := studioRevision([]byte(studioScore))
	response := studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: result.Source})
	if response.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", response.Code, response.Body.String())
	}
	if response := studioCall(t, handler, "/api/source", studioEdit{Revision: revision, Source: result.Source}); response.Code != http.StatusConflict {
		t.Fatal("stale preview replaced source")
	}
	current, _ := os.ReadFile(path)
	response = studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(current)})
	if response.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", response.Code, response.Body.String())
	}
	if source, _ := os.ReadFile(path); string(source) != studioScore {
		t.Fatal("undo did not restore prior source")
	}
	if response := studioCall(t, handler, "/studio-transcribe.js", nil); response.Code != 200 {
		t.Fatal("transcription script unavailable")
	}
}

func TestStudioTranscriptionRejectsForeignConnections(t *testing.T) {
	handler, _ := studioTestHandler(t)
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.1:1234" },
		func(r *http.Request) { r.Host = "other.example:1234" },
		func(r *http.Request) { r.Header.Set("Origin", "http://other.example") },
		func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:1234/path") },
		func(r *http.Request) { r.Header.Set("Origin", "http://user@127.0.0.1:1234") },
		func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") },
	} {
		r := transcriptionUpload(t, []byte("invalid"))
		change(r)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("foreign request accepted: %d", w.Code)
		}
	}
}

func TestStudioTranscriptionRemovesTemporaryUploads(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	handler, _ := studioTestHandler(t)
	for _, canceled := range []bool{false, true} {
		r := transcriptionUpload(t, bytes.Repeat([]byte{0}, 2<<20))
		if canceled {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			r = r.WithContext(ctx)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if !canceled && w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid WAV accepted: %d", w.Code)
		}
		entries, err := os.ReadDir(temp)
		if err != nil || len(entries) != 0 {
			t.Fatal("temporary audio retained", entries, err)
		}
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, _ := form.CreateFormFile("wav", "melody.wav")
	_, _ = file.Write(bytes.Repeat([]byte{0}, 2<<20))
	r := httptest.NewRequest(http.MethodPost, "/api/transcribe", &body)
	r.Host, r.RemoteAddr = "127.0.0.1:1234", "127.0.0.1:4321"
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("truncated upload accepted: %d", w.Code)
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed multipart retained temporary audio", entries, err)
	}
}

func TestStudioTranscriptionReadsCompletedNativeTake(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	audio, err := recording.DecodeWAV(transcriptionWAV())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.takes.Begin("vox", "main", studioRevision([]byte(audioTakeScore)), audio.Rate, 1)
	if err != nil {
		t.Fatal(err)
	}
	for at := 0; at < len(audio.PCM); at += 2048 {
		end := min(at+2048, len(audio.PCM))
		b := capture.Block{Frames: end - at, SampleRate: audio.Rate, Layout: capture.LayoutMono, EngineFrame: int64(at), DeviceFrame: uint64(at)}
		if err := s.takes.Write(id, capture.RecordedBlock{Timing: b, RawFrame: uint64(at), Placement: capture.Place(b)}, [][]float32{audio.PCM[at:end]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.takes.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err := s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"takeId": id, "options": transcription.DefaultOptions()})
	r := httptest.NewRequest(http.MethodPost, "/api/transcribe", bytes.NewReader(data))
	r.Host, r.RemoteAddr = "127.0.0.1:1234", "127.0.0.1:4321"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("native transcription: %d %s", w.Code, w.Body.String())
	}
	var result transcription.Result
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Notes) != 4 {
		t.Fatal("native recording notes unavailable", err)
	}
	if source, _ := os.ReadFile(s.path); string(source) != audioTakeScore {
		t.Fatal("transcription changed existing source")
	}
	if take, err := s.takes.Get(id); err != nil || take.Asset.Path == "" {
		t.Fatal("transcription removed durable recording", err)
	}
}
