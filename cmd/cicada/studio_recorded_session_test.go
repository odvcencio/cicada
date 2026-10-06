package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/project"
)

// A microphone-style stereo take: fifteen irregular/noisy attacks over pitched
// room noise near -50 dB. Each attack has a close reflection, not another hit.
func microphoneStereo(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../../host/recording/testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	source, err := recording.DecodeWAV(data)
	if err != nil {
		t.Fatal(err)
	}
	rate := 48000
	frames := len(source.PCM) * rate / source.Rate
	mono := make([]float32, frames)
	for i := range mono {
		noise := .0025 * math.Sin(2*math.Pi*173*float64(i)/float64(rate))
		hit := float64(source.PCM[i*source.Rate/rate])
		reflection := 0.0
		if i > rate*35/1000 {
			reflection = .25 * float64(source.PCM[(i-rate*35/1000)*source.Rate/rate])
		}
		mono[i] = float32(hit + reflection + noise)
	}
	wav := recording.EncodeWAV(make([]float32, frames*2), rate)
	binary.LittleEndian.PutUint16(wav[22:], 2)
	binary.LittleEndian.PutUint32(wav[28:], uint32(rate*8))
	binary.LittleEndian.PutUint16(wav[32:], 8)
	for i, x := range mono {
		binary.LittleEndian.PutUint32(wav[44+i*8:], math.Float32bits(x))
		binary.LittleEndian.PutUint32(wav[48+i*8:], math.Float32bits(x*.8))
	}
	return wav
}

func TestNativeMicrophoneInstrumentToStudioAndRender(t *testing.T) {
	path := copyStudioProject(t)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	audio, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	s.transport.audio = audio
	s.transport.sampleRate = 48000
	s.transport.audioOptions = studioAudioOptions{InputEnabled: true, MonitorMuted: true, MonitorMode: "stereo"}
	h := s.routes()
	original, _ := os.ReadFile(path)
	start := studioCall(t, h, "/api/instrument-capture", studioEdit{Revision: studioRevision(original), Action: "start"})
	if start.Code != 200 {
		t.Fatal(start.Body.String())
	}
	wav := microphoneStereo(t)
	frames := (len(wav) - 44) / 8
	var l, r, outL, outR [256]float32
	for at := 0; at < frames; at += 256 {
		n := min(256, frames-at)
		for i := 0; i < n; i++ {
			l[i] = math.Float32frombits(binary.LittleEndian.Uint32(wav[44+(at+i)*8:]))
			r[i] = math.Float32frombits(binary.LittleEndian.Uint32(wav[48+(at+i)*8:]))
		}
		simulatedCapturePeriod(t, audio, uint64(at), [][]float32{l[:n], r[:n]}, [][]float32{outL[:n], outR[:n]})
		if at%(256*16) == 0 {
			deadline := time.Now().Add(time.Second)
			for s.captureRecorder.Snapshot().WrittenFrames < uint64(at+n) && time.Now().Before(deadline) {
				if fault := s.captureRecorder.Snapshot().Error; fault != "" {
					t.Fatal(fault)
				}
				time.Sleep(time.Millisecond)
			}
			if s.captureRecorder.Snapshot().WrittenFrames < uint64(at+n) {
				t.Fatal("capture writer did not drain")
			}
		}
	}
	stopped := studioCall(t, h, "/api/instrument-capture", studioEdit{Revision: studioRevision(original), Action: "stop"})
	if stopped.Code != 200 {
		t.Fatal(stopped.Body.String())
	}
	var preview struct {
		Pin  string `json:"sha256"`
		Hits []recording.Hit
	}
	if json.Unmarshal(stopped.Body.Bytes(), &preview) != nil || len(preview.Hits) != 15 {
		t.Fatalf("expected fifteen real hits: %s", stopped.Body.String())
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, original) {
		t.Fatal("review changed score before building")
	}
	hit := studioCall(t, h, "/api/instrument-hit?sha256="+preview.Pin+"&hit=1", nil)
	if hit.Code != 200 {
		t.Fatal("cannot play reviewed hit")
	}
	built := studioCall(t, h, "/api/instrument-build", studioEdit{Revision: studioRevision(original), TakeID: preview.Pin, Scene: "main"})
	if built.Code != 200 {
		t.Fatal(built.Body.String())
	}
	p, err := loadProject(path)
	if err != nil || len(p.Samplers) != 1 || !recordedTrackExists(p, "recorded_track", false) {
		t.Fatal("recorded track missing", err)
	}
	studioRenderParity(t, path)
	fitted := studioCall(t, h, "/api/instrument-fit", map[string]any{"revision": s.recordedRevision(), "sha256": s.recordedSampled, "hit": 11})
	if fitted.Code != 200 {
		t.Fatal(fitted.Body.String())
	}
	p, err = loadProject(path)
	if err != nil || len(p.Samplers) != 2 || !recordedTrackExists(p, "recorded_model_track", true) {
		t.Fatal("model replaced sampled default", err)
	}
	if s.recordedSampled == s.recordedModeled || s.recordedSampled == "" {
		t.Fatal("sampled pack lost")
	}
	studioRenderParity(t, path)
	undo := studioCall(t, h, "/api/undo", studioEdit{Revision: s.recordedRevision()})
	if undo.Code != 200 {
		t.Fatal(undo.Body.String())
	}
	p, _ = loadProject(path)
	if len(p.Samplers) != 1 {
		t.Fatal("model undo did not restore sampled score")
	}
	undo = studioCall(t, h, "/api/undo", studioEdit{Revision: s.recordedRevision()})
	if undo.Code != 200 {
		t.Fatal(undo.Body.String())
	}
	source, _ := os.ReadFile(path)
	if !bytes.Equal(source, original) {
		t.Fatal("recording undo did not restore project")
	}
}

func TestRecordedReviewRemovalAndStaleBuild(t *testing.T) {
	s, err := newStudio(copyStudioProject(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	var body bytes.Buffer
	// Reuse the WAV upload contract with a preview action.
	form := newInstrumentForm(t, &body, microphoneStereo(t), "analyze")
	request := httptest.NewRequest(http.MethodPost, "/api/instrument-record", &body)
	request.Host = "127.0.0.1:1234"
	request.Header.Set("Content-Type", form)
	response := httptest.NewRecorder()
	s.routes().ServeHTTP(response, request)
	var preview struct {
		Pin  string `json:"sha256"`
		Hits []recording.Hit
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || len(preview.Hits) != 15 {
		t.Fatal(response.Body.String())
	}
	removed := studioCall(t, s.routes(), "/api/instrument-build", studioEdit{Revision: s.recordedRevision(), TakeID: preview.Pin, Action: "remove", Index: 3})
	if removed.Code != 200 || !bytes.Contains(removed.Body.Bytes(), []byte(`"kept":14`)) {
		t.Fatal(removed.Body.String())
	}
	stale := studioCall(t, s.routes(), "/api/instrument-build", studioEdit{Revision: "stale", TakeID: preview.Pin, Scene: "main"})
	if stale.Code != 409 {
		t.Fatal("stale build admitted", stale.Body.String())
	}
}

func newInstrumentForm(t *testing.T, body *bytes.Buffer, data []byte, action string) string {
	t.Helper()
	form := multipart.NewWriter(body)
	form.WriteField("action", action)
	file, err := form.CreateFormFile("wav", "microphone.wav")
	if err != nil {
		t.Fatal(err)
	}
	file.Write(data)
	form.Close()
	return form.FormDataContentType()
}

func recordedTrackExists(p *project.Project, id string, muted bool) bool {
	for _, track := range p.Tracks {
		if track.ID == id && track.Mixer.Mute == muted {
			return true
		}
	}
	return false
}

func TestRecordedInstrumentInPartSceneUndo(t *testing.T) {
	path := copyStudioProject(t)
	original, _ := os.ReadFile(path)
	source := bytes.Replace(original, []byte("scene main { lead = riff beat = taps }\n"), nil, 1)
	if err := os.WriteFile(path, source, 0644); err != nil {
		t.Fatal(err)
	}
	partPath := filepath.Join(filepath.Dir(path), "parts/patterns.cicada")
	part, _ := os.ReadFile(partPath)
	part = append(part, []byte("scene main { lead = riff beat = taps }\n")...)
	if err := os.WriteFile(partPath, part, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	data, err := os.ReadFile("../../host/recording/testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	response := instrumentUpload(t, s.routes(), data, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	p, err := loadProject(path)
	if err != nil || !recordedTrackExists(p, "recorded_track", false) {
		t.Fatal("part scene instrument missing", err)
	}
	if p.Scenes[0].Bindings["recorded_track"] != "recorded_taps" {
		t.Fatal("current part scene was not assigned")
	}
	undo := studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: s.recordedRevision()})
	if undo.Code != 200 {
		t.Fatal(undo.Body.String())
	}
	mainNow, _ := os.ReadFile(path)
	partNow, _ := os.ReadFile(partPath)
	if !bytes.Equal(mainNow, source) || !bytes.Equal(partNow, part) {
		t.Fatal("cross-file undo changed authored source")
	}
	redo := studioCall(t, s.routes(), "/api/redo", studioEdit{Revision: s.recordedRevision()})
	if redo.Code != 200 {
		t.Fatal(redo.Body.String())
	}
	studioRenderParity(t, path)
}
