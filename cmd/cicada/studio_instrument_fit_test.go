package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/modalfit"
	"m31labs.dev/cicada/host/recording"
)

func TestStudioInstrumentFitAndOfflineModel(t *testing.T) {
	handler, path := studioTestHandler(t)
	fixture := "../../host/recording/testdata/pencil-taps.wav"
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	upload := instrumentUpload(t, handler, data, "")
	var source struct {
		Pin string `json:"sha256"`
	}
	if err = json.Unmarshal(upload.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	fit := studioCall(t, handler, "/api/instrument-fit", map[string]any{"sha256": source.Pin, "hit": 11})
	if fit.Code != 200 {
		t.Fatal("fit", fit.Code, fit.Body.String())
	}
	var model struct {
		Pin       string         `json:"sha256"`
		Model     modalfit.Model `json:"model"`
		Path      string         `json:"manifestPath"`
		ModelHash string         `json:"modelSHA256"`
	}
	if err = json.Unmarshal(fit.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if model.Pin == source.Pin || len(model.Model.Modes) == 0 {
		t.Fatal("missing fitted model")
	}
	modelJSON, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.Dir(model.Path), "model.json"))
	if err != nil || recording.Digest(modelJSON) != model.ModelHash {
		t.Fatal("model checksum", err)
	}
	for _, note := range []int{0, 60, 72, 127} {
		play := studioCall(t, handler, "/api/instrument-audition", map[string]any{"sha256": model.Pin, "note": note, "velocity": 80, "cycle": 0})
		if play.Code != 200 {
			t.Fatal("modeled audition", note, play.Body.String())
		}
		audio, err := recording.DecodeWAV(play.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		expected, err := model.Model.Render(note, 80, 0, 48000)
		if err != nil || !bytes.Equal(play.Body.Bytes(), recording.EncodeWAV(expected, audio.Rate)) {
			t.Fatal("audition did not use fitted modal engine", err)
		}
	}
	for _, hit := range []int{0, 16} {
		if r := studioCall(t, handler, "/api/instrument-fit", map[string]any{"sha256": source.Pin, "hit": hit}); r.Code != 422 {
			t.Fatal("invalid hit accepted")
		}
	}
	dir := filepath.Join(t.TempDir(), "model")
	var output bytes.Buffer
	if err = fitModelCommand([]string{"-o", dir, "--name", "pencil_model", "--hit", "11", fixture}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "fitted modes") || !strings.Contains(output.String(), "sha256 =") {
		t.Fatal("missing model receipt")
	}
	if err = fitModelCommand([]string{"-o", dir, "--hit", "0", fixture}, &output); err == nil {
		t.Fatal("CLI invalid hit accepted")
	}
}
