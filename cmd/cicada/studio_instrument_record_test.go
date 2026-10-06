package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/recording"
)

func instrumentUpload(t *testing.T, handler http.Handler, data []byte, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{"name": "recorded", "root": "60", "layers": "3", "autoPitch": "false"} {
		if err := form.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := form.CreateFormFile("wav", "taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	form.Close()
	r := httptest.NewRequest("POST", "/api/instrument-record", &body)
	r.Host = "127.0.0.1:1234"
	r.Header.Set("Content-Type", form.FormDataContentType())
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestStudioRecordedInstrument(t *testing.T) {
	handler, path := studioTestHandler(t)
	wav, err := os.ReadFile("../../host/recording/testdata/pencil-taps.wav")
	if err != nil {
		t.Fatal(err)
	}
	response := instrumentUpload(t, handler, wav, "")
	if response.Code != 200 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Pin         string             `json:"sha256"`
		Hits        []recording.Hit    `json:"hits"`
		Manifest    recording.Manifest `json:"manifest"`
		Path        string             `json:"manifestPath"`
		Declaration string             `json:"declaration"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 15 || len(result.Manifest.Zones) != 15 || !strings.Contains(result.Declaration, result.Pin) {
		t.Fatal("missing detected hits or pinned declaration")
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(path), result.Path))
	if err != nil || recording.Digest(manifest) != result.Pin {
		t.Fatal("published pack pin", err)
	}
	if source, err := os.ReadFile(path); err != nil || !strings.Contains(string(source), "track recorded_track recorded") {
		t.Fatal("recording did not appear in the open score")
	}
	asset := studioCall(t, handler, "/"+result.Path, nil)
	if asset.Code != 200 || !bytes.Equal(asset.Body.Bytes(), manifest) {
		t.Fatal("pack asset serving", asset.Code)
	}
	var takes [][]byte
	for cycle := 0; cycle < 6; cycle++ {
		play := studioCall(t, handler, "/api/instrument-audition", map[string]any{"sha256": result.Pin, "note": 60, "velocity": 80, "cycle": cycle})
		if play.Code != 200 || play.Header().Get("Content-Type") != "audio/wav" {
			t.Fatalf("audition: %d %s", play.Code, play.Body.String())
		}
		if _, err := recording.DecodeWAV(play.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
		takes = append(takes, append([]byte(nil), play.Body.Bytes()...))
	}
	if bytes.Equal(takes[0], takes[1]) || !bytes.Equal(takes[0], takes[5]) {
		t.Fatal("round robin cycle")
	}
	soft := studioCall(t, handler, "/api/instrument-audition", map[string]any{"sha256": result.Pin, "note": 60, "velocity": 25, "cycle": 0})
	if soft.Code != 200 || bytes.Equal(soft.Body.Bytes(), takes[0]) {
		t.Fatal("velocity response")
	}
	if response := studioCall(t, handler, "/api/instrument-audition", map[string]any{"sha256": result.Pin, "note": 200, "velocity": 80, "cycle": 0}); response.Code != 422 {
		t.Fatal("invalid MIDI note accepted")
	}
	if response := instrumentUpload(t, handler, wav, "https://other.example"); response.Code != 403 {
		t.Fatal("foreign origin accepted")
	}
	if response := instrumentUpload(t, handler, []byte("bad WAV"), ""); response.Code != 400 {
		t.Fatal("invalid WAV accepted")
	}
	if response := studioCall(t, handler, "/studio-instrument-record.js", nil); response.Code != 200 {
		t.Fatal("missing UI script")
	}
}

func TestOfflineRecordPack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pencil")
	var output bytes.Buffer
	if err := recordPackCommand([]string{"-o", dir, "--name", "pencil", "--auto-pitch=false", "../../host/recording/testdata/pencil-taps.wav"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "15 hits") || !strings.Contains(output.String(), "sha256 =") {
		t.Fatal("missing CLI receipt")
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := recordPackCommand([]string{"-o", dir, "--layers", "0", "../../host/recording/testdata/pencil-taps.wav"}, &output); err == nil {
		t.Fatal("invalid layers accepted")
	}
}
