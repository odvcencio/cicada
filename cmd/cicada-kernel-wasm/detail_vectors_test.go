package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

const detailVectorFile = "../../examples/detail/vectors.json"

// detailVectorRates are the rates every detail score is rendered at.
var detailVectorRates = []int{44_100, 48_000, 96_000}

// detailPCM24 renders a project the way the parity test does (128-frame blocks,
// the song, then three seconds of tail) and returns the SHA-256 of its PCM24.
func detailPCM24(t *testing.T, path string, rate int) string {
	t.Helper()
	score, diagnostics, err := project.LoadScore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %v", diagnostics)
	}
	const block = 128
	cfg, err := project.CompileEngine(p, rate, block)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var endTick int64
	for _, entry := range cfg.Song {
		endTick += int64(entry.Bars) * seq.TicksPerBar
	}
	tempo, err := project.NewTempoMap(rate, cfg.BPMMilli, project.SongTempoChanges(p))
	if err != nil || endTick == 0 {
		t.Fatalf("song clock: %v", err)
	}
	frames := int(tempo.SampleAtTick(endTick)) + 3*rate
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play command refused")
	}
	hash := sha256.New()
	var left, right [block]float32
	var pcm [block * 6]byte
	sounded := false
	for at := 0; at < frames; at += block {
		n := min(block, frames-at)
		e.Render(left[:n], right[:n])
		for i := 0; i < n; i++ {
			for channel, value := range [...]float32{left[i], right[i]} {
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					t.Fatalf("nonfinite sample at frame %d", at+i)
				}
				index := i*6 + channel*3
				v := int32(math.RoundToEven(float64(value) * 8388607))
				v = max(-8388608, min(8388607, v))
				pcm[index], pcm[index+1], pcm[index+2] = byte(v), byte(v>>8), byte(v>>16)
				sounded = sounded || value != 0
			}
		}
		hash.Write(pcm[:n*6])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("native fault: %+v", m)
			}
		}
	}
	if !sounded {
		t.Fatal("silent render")
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// TestDetailVectors pins the PCM24 SHA-256 of every examples/detail score at
// 44.1, 48 and 96 kHz. The same bytes must come out on amd64, arm64 and in the
// WASM module: CI runs this test on arm64 and the parity job compares the PCM
// itself. Regenerate with CICADA_UPDATE_VECTORS=1 after a deliberate change.
func TestDetailVectors(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "examples", "detail", "*.cicada"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("discover detail examples: %v (%d scores)", err, len(paths))
	}
	got := map[string]string{}
	for _, path := range paths {
		for _, rate := range detailVectorRates {
			t.Run(fmt.Sprintf("%s-%d", filepath.Base(path), rate), func(t *testing.T) {
				got[fmt.Sprintf("%s-%d", filepath.Base(path), rate)] = detailPCM24(t, path, rate)
			})
		}
	}
	if os.Getenv("CICADA_UPDATE_VECTORS") != "" {
		data, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(detailVectorFile, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(detailVectorFile)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	for name, hash := range got {
		if want[name] != hash {
			t.Errorf("%s: PCM24 sha256 %s, pinned %s", name, hash, want[name])
		}
	}
	if len(want) != len(got) {
		t.Errorf("%d pinned vectors, %d rendered", len(want), len(got))
	}
}
