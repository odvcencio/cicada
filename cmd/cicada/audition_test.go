package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAuditionCommandJSONAndImages(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "first-acid.cicada")
	var jsons [2][]byte
	var images [2][3][]byte
	for run := range jsons {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		if err := auditionCommand([]string{score, "-o", dir, "--bars", "4", "--json"}, &stdout, &stderr); err != nil {
			t.Fatalf("%v\n%s", err, stderr.String())
		}
		jsons[run] = stdout.Bytes()
		for i, name := range []string{"spectrogram.png", "chroma.png", "rhythm.png"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG")) {
				t.Fatalf("%s: %v", name, err)
			}
			images[run][i] = data
		}
	}
	if !bytes.Equal(jsons[0], jsons[1]) {
		t.Fatal("JSON differs between runs")
	}
	for i := range images[0] {
		if !bytes.Equal(images[0][i], images[1][i]) {
			t.Fatalf("image %d differs between runs", i)
		}
	}
	var report map[string]any
	if err := json.Unmarshal(jsons[0], &report); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"format", "title", "sample_rate", "score", "heard_key", "bars", "tracks", "sections", "harmony_mismatches", "images"} {
		if _, ok := report[key]; !ok {
			t.Errorf("JSON lacks %q", key)
		}
	}
	if report["format"] != "cicada.audition/1" {
		t.Errorf("format %v", report["format"])
	}
	if bars, _ := report["bars"].([]any); len(bars) != 4 {
		t.Errorf("%d bars, want 4", len(bars))
	}
}

func TestAuditionCommandText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	score := filepath.Join("..", "..", "examples", "first-acid.cicada")
	if err := auditionCommand([]string{score, "--from", "2", "--bars", "2"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"heard key (pitched stems):", "full mix key:", "audibility:", "harmony per bar", "tracks (", "sections:", "bass"} {
		if !bytes.Contains(stdout.Bytes(), []byte(want)) {
			t.Errorf("text report lacks %q:\n%s", want, stdout.String())
		}
	}
	if err := auditionCommand([]string{}, &stdout, &stderr); err == nil {
		t.Error("missing score accepted")
	}
}
