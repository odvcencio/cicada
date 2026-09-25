package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
)

const liveTestScore = "title \"live\"\ntempo 120\nkey a minor\ntrack bass acid {}\npattern pulse acid steps = 16 { 1 . . . 5 . . . 1 . . . 7 . . . }\nscene main { bass = pulse }\nsong { main*8 }\n"

func TestLiveReaderProducesStereoAudioFromScore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(liveTestScore), 0644); err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, 8192*8)
	if _, err := io.ReadFull(stream, pcm); err != nil {
		t.Fatal(err)
	}
	var energy float64
	for frame := 0; frame < len(pcm)/8; frame++ {
		left := math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8:]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8+4:]))
		if math.IsNaN(float64(left)) || math.IsNaN(float64(right)) {
			t.Fatal("live reader produced nonfinite PCM")
		}
		energy += float64(left*left + right*right)
	}
	if energy <= 1e-6 {
		t.Fatal("compiled live score produced silent PCM")
	}
}

func TestLiveWatchKeepsLastGoodScoreAndQueuesValidEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(path, []byte(liveTestScore), 0644); err != nil {
		t.Fatal(err)
	}
	initialHash, err := playSourceHash(path)
	if err != nil {
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
	var log bytes.Buffer
	watcher := liveScoreWatcher{path: path, last: initialHash, stream: stream, output: &log}
	if err := os.WriteFile(path, []byte("title \"broken\"\npattern pulse { ? }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	watcher.poll()
	if !strings.Contains(log.String(), "continuing previous score") {
		t.Fatalf("invalid edit did not report last-good recovery: %s", log.String())
	}
	if _, err := io.CopyN(io.Discard, stream, 96_000*8); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := io.ReadFull(stream, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		t.Fatalf("invalid edit landed: %+v", event)
	default:
	}
	valid := strings.Replace(liveTestScore, "tempo 120", "tempo 90", 1)
	if err := os.WriteFile(path, []byte(valid), 0644); err != nil {
		t.Fatal(err)
	}
	watcher.poll()
	if !strings.Contains(log.String(), "queued for next bar") {
		t.Fatalf("valid edit was not queued: %s", log.String())
	}
	if _, err := io.CopyN(io.Discard, stream, (96_000-2)*8); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stream, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		t.Fatalf("valid edit landed before next bar: %+v", event)
	default:
	}
	if _, err := io.ReadFull(stream, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream.Events():
		if event.Bar != 3 {
			t.Fatalf("edit landed at bar %d, want 3", event.Bar)
		}
	default:
		t.Fatal("valid edit never landed")
	}
}

func TestLiveWatchIncludesNearestManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(liveTestScore), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := playSourceHash(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cicada.mod"), []byte("project live\ncicada 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := playSourceHash(path)
	if err != nil || before == after {
		t.Fatalf("manifest did not change live source hash: %v", err)
	}
}
