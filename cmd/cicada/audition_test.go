package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	for _, key := range []string{"loudness", "bands", "activity", "peaks", "pictures"} {
		if _, ok := report[key]; !ok {
			t.Errorf("JSON lacks %q", key)
		}
	}
	// images keeps its three names; pictures lists every file written.
	if images, _ := json.Marshal(report["images"]); string(images) != `["spectrogram.png","chroma.png","rhythm.png"]` {
		t.Errorf("images %s", images)
	}
	var files []string
	for _, p := range report["pictures"].([]any) {
		files = append(files, p.(map[string]any)["file"].(string))
	}
	if strings.Join(files, " ") != "spectrogram.png chroma.png rhythm.png energy.png" {
		t.Errorf("pictures %v", files)
	}
}

func TestAuditionCommandUnknownTrack(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "first-acid.cicada")
	var stdout, stderr bytes.Buffer
	if err := auditionCommand([]string{score, "--bars", "1", "--tracks", "bass,nope", "--json"}, &stdout, &stderr); err == nil {
		t.Fatal("unknown track accepted")
	}
	var document struct {
		Schema string `json:"schema"`
		Error  struct {
			Code        string   `json:"code"`
			Message     string   `json:"message"`
			ValidTracks []string `json:"valid_tracks"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if document.Schema != "cicada.audition.v1" || document.Error.Code != "unknown_track" || !strings.Contains(document.Error.Message, `"nope"`) || len(document.Error.ValidTracks) == 0 {
		t.Errorf("%s", stdout.String())
	}
}

func TestAuditionCommandTracksAndPages(t *testing.T) {
	score := filepath.Join("..", "..", "examples", "first-acid.cicada")
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if err := auditionCommand([]string{score, "--bars", "4", "--tracks", "bass", "--image-width", "200", "--peaks", "2", "-o", dir, "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	var report struct {
		Tracks         []struct{ ID string } `json:"tracks"`
		SelectedTracks []string              `json:"selected_tracks"`
		Peaks          []any                 `json:"peaks"`
		Pictures       []struct {
			File    string `json:"file"`
			FromBar int    `json:"from_bar"`
			ToBar   int    `json:"to_bar"`
			Width   int    `json:"width"`
		} `json:"pictures"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Tracks) != 1 || report.Tracks[0].ID != "bass" || strings.Join(report.SelectedTracks, ",") != "bass" || len(report.Peaks) != 2 {
		t.Errorf("tracks %v, selected %v, %d peaks", report.Tracks, report.SelectedTracks, len(report.Peaks))
	}
	pages := 0
	for _, p := range report.Pictures {
		// rhythm.png has no time axis: its 16 cells keep their width.
		if p.Width > 200 && p.File != "rhythm.png" {
			t.Errorf("%s is %d wide", p.File, p.Width)
		}
		if strings.HasPrefix(p.File, "spectrogram-bars-") {
			pages++
			if _, err := os.Stat(filepath.Join(dir, p.File)); err != nil || !strings.Contains(p.File, fmt.Sprintf("%03d-%03d", p.FromBar, p.ToBar)) {
				t.Errorf("page %+v: %v", p, err)
			}
		}
	}
	if pages < 2 {
		t.Errorf("%d spectrogram pages for 4 bars at 200 pixels", pages)
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
