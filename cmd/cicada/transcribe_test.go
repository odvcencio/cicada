package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/host/transcription"
)

func TestTranscribeCLIProducesScoreAndPreservesOutput(t *testing.T) {
	const rate = 16000
	pcm := make([]float32, rate)
	for i := rate / 10; i < rate*8/10; i++ {
		pcm[i] = float32(.2 * math.Sin(2*math.Pi*220*float64(i)/rate))
	}
	dir := t.TempDir()
	path, destination := filepath.Join(dir, "voice.wav"), filepath.Join(dir, "melody.cicada")
	if err := os.WriteFile(path, recording.EncodeWAV(pcm, rate), 0600); err != nil {
		t.Fatal(err)
	}
	var output, report bytes.Buffer
	if err := transcribeCommand([]string{path, "--tempo", "120", "--key", "a minor", "--report", "-o", destination}, &output, &report); err != nil {
		t.Fatal(err)
	}
	var result transcription.Result
	if err := json.Unmarshal(report.Bytes(), &result); err != nil || len(result.Notes) != 1 || !strings.HasPrefix(result.Source, "cicada 2") {
		t.Fatal("missing score or notes", err, report.String())
	}
	saved, err := os.ReadFile(destination)
	if err != nil || string(saved) != result.Source {
		t.Fatal("output differs from validated score", err)
	}
	if err := transcribeCommand([]string{path, "-o", destination}, &output, &report); err == nil {
		t.Fatal("existing output was overwritten")
	}
	stillSaved, _ := os.ReadFile(destination)
	if !bytes.Equal(saved, stillSaved) {
		t.Fatal("existing output changed")
	}
}

func TestTranscribeCLIRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{nil, {"voice.wav", "--grid", "1/32"}, {"voice.wav", "--grid", "bad"}, {"voice.wav", "another.wav"}, {"--unknown"}} {
		if err := transcribeCommand(args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted invalid arguments", args)
		}
	}
	var help, errors bytes.Buffer
	if handled, code := handleCLIHelp([]string{"transcribe", "--help"}, &help, &errors); !handled || code != 0 || !strings.Contains(help.String(), "--tempo") {
		t.Fatal("transcribe help missing")
	}
}
