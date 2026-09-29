//go:build windows

package audiobackend

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/tymbal"
)

const engineSoakChannels = 2
const engineSoakFrames = 256
const engineSoakMaxCallbackFrames = 4096

var errEngineSoakCallbackFormat = errors.New("unexpected Tymbal engine-soak callback format")

type engineSoakRender struct {
	player        *liveplay.Player
	pcm           [engineSoakMaxCallbackFrames * engineSoakChannels * 4]byte
	callbackCount atomic.Uint64
	deadlineMiss  atomic.Uint64
	engineSound   atomic.Bool
	nonzeroOutput atomic.Bool
	histogram     engineSoakHistogram
	maxNanos      atomic.Uint64
	deadlineNanos uint64
}

func (r *engineSoakRender) callback(_ [][]float32, output [][]float32) error {
	if len(output) != engineSoakChannels || len(output[0]) > engineSoakMaxCallbackFrames || len(output[1]) != len(output[0]) {
		for _, channel := range output {
			clear(channel)
		}
		return errEngineSoakCallbackFormat
	}

	start := time.Now()
	read, err := r.player.Read(r.pcm[:len(output[0])*engineSoakChannels*4])
	elapsed := uint64(time.Since(start).Nanoseconds())
	r.callbackCount.Add(1)
	r.histogram.record(elapsed)
	updateEngineSoakMax(&r.maxNanos, elapsed)
	if elapsed > r.deadlineNanos {
		r.deadlineMiss.Add(1)
	}
	if err != nil || read != len(output[0])*engineSoakChannels*4 {
		for _, channel := range output {
			clear(channel)
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return err
	}

	for frame := range output[0] {
		left := math.Float32frombits(binary.LittleEndian.Uint32(r.pcm[frame*8:]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(r.pcm[frame*8+4:]))
		if left != 0 || right != 0 {
			r.engineSound.Store(true)
		}
		output[0][frame], output[1][frame] = left, right
	}

	// This is the final operation on the device buffer: engine audio is measured
	// above but is never allowed to reach the WASAPI endpoint.
	for _, channel := range output {
		clear(channel)
		for _, sample := range channel {
			if sample != 0 {
				r.nonzeroOutput.Store(true)
			}
		}
	}
	return nil
}

func updateEngineSoakMax(maximum *atomic.Uint64, value uint64) {
	for current := maximum.Load(); value > current; current = maximum.Load() {
		if maximum.CompareAndSwap(current, value) {
			return
		}
	}
}

func TestTymbalWASAPIEngineSoak(t *testing.T) {
	durationText := os.Getenv("CICADA_AUDIO_WASAPI_ENGINE_SOAK")
	if durationText == "" {
		t.Skip("set CICADA_AUDIO_WASAPI_ENGINE_SOAK to a duration such as 60s or 1h on Windows to run the engine soak")
	}
	duration, err := time.ParseDuration(durationText)
	if err != nil || duration <= 0 {
		t.Fatalf("invalid CICADA_AUDIO_WASAPI_ENGINE_SOAK duration %q", durationText)
	}

	restore, _, _ := tymbal.RaiseProcessThreads(1)
	defer restore()

	root := engineSoakRepoRoot(t)
	backend, err := New(Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Channels: engineSoakChannels, FramesPerPeriod: engineSoakFrames}
	rate, err := backend.SampleRate(config)
	if err != nil {
		t.Fatalf("get Tymbal sample rate: %v", err)
	}
	score, err := loadEngineSoakScore(filepath.Join(root, "examples", "first-acid.cicada"), rate)
	if err != nil {
		t.Fatalf("compile engine-soak score at %d Hz: %v", rate, err)
	}
	player, err := liveplay.New(score, rate)
	if err != nil {
		t.Fatalf("create liveplay engine: %v", err)
	}
	defer player.Close()

	render := &engineSoakRender{player: player}
	config.SampleRate = rate
	stream, err := backend.Open(config, render.callback)
	if err != nil {
		t.Fatalf("open Tymbal WASAPI stream: %v", err)
	}
	defer stream.Close()
	format := stream.Format()
	period := time.Duration(format.FramesPerPeriod) * time.Second / time.Duration(format.SampleRate)
	render.deadlineNanos = uint64(period.Nanoseconds())
	if err := stream.Start(); err != nil {
		t.Fatalf("start Tymbal WASAPI stream: %v", err)
	}
	time.Sleep(duration)
	stream.Pause()

	callbacks := render.callbackCount.Load()
	misses := render.deadlineMiss.Load()
	minimumCallbacks := uint64(duration / period)
	minimumCallbacks -= minimumCallbacks / 100
	p50 := render.histogram.percentile(50, callbacks)
	p99 := render.histogram.percentile(99, callbacks)
	p999 := engineSoakPercentile(&render.histogram, 999, callbacks, 1000)
	maximum := render.maxNanos.Load()
	t.Logf("Tymbal WASAPI engine soak duration=%s sample_rate=%d frames_per_period=%d callbacks=%d p50_us=%.3f p99_us=%.3f p999_us=%.3f max_us=%.3f deadline_misses=%d", duration, format.SampleRate, format.FramesPerPeriod, callbacks, float64(p50)/1000, float64(p99)/1000, float64(p999)/1000, float64(maximum)/1000, misses)
	t.Logf("engine_nonzero_samples=%t output_nonzero_samples=%t liveplay_underrun_fault_counters=not-exposed", render.engineSound.Load(), render.nonzeroOutput.Load())

	if err := stream.Err(); err != nil {
		t.Errorf("Tymbal WASAPI stream error: %v", err)
	}
	if callbacks < minimumCallbacks {
		t.Errorf("callbacks %d below 99%% of expected minimum %d", callbacks, minimumCallbacks)
	}
	if misses != 0 {
		t.Errorf("%d callback deadline misses (deadline %s)", misses, period)
	}
	if render.nonzeroOutput.Load() {
		t.Error("non-zero sample detected in device output buffer")
	}
	if !render.engineSound.Load() {
		t.Error("liveplay engine rendered no non-zero samples")
	}
}

func engineSoakPercentile(histogram *engineSoakHistogram, numerator uint64, total uint64, denominator uint64) uint64 {
	if total == 0 {
		return 0
	}
	rank := (total*numerator + denominator - 1) / denominator
	var seen uint64
	for bucket := range histogram.buckets {
		seen += histogram.buckets[bucket].Load()
		if seen >= rank {
			return uint64(1) << bucket
		}
	}
	return 0
}

func engineSoakRepoRoot(t *testing.T) string {
	t.Helper()
	if _, source, _, ok := runtime.Caller(0); ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
		if _, err := os.Stat(filepath.Join(root, "examples", "first-acid.cicada")); err == nil {
			return root
		}
	}
	working, err := os.Getwd()
	if err == nil {
		for dir := working; ; dir = filepath.Dir(dir) {
			if _, statErr := os.Stat(filepath.Join(dir, "examples", "first-acid.cicada")); statErr == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	t.Fatalf("cannot locate repository root with examples/first-acid.cicada (cwd error: %v)", err)
	return ""
}

func loadEngineSoakScore(path string, rate int) (liveplay.Score, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return liveplay.Score{}, err
	}
	sourceScore, diagnostics := notation.Parse(data)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return liveplay.Score{}, fmt.Errorf("%s:%d:%d: %s", diagnostic.Code, diagnostic.Position.Line, diagnostic.Position.Column, diagnostic.Message)
		}
	}
	model, diagnostics := project.FromScore(sourceScore)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return liveplay.Score{}, fmt.Errorf("%s:%d:%d: %s", diagnostic.Code, diagnostic.Position.Line, diagnostic.Position.Column, diagnostic.Message)
		}
	}
	if rate == 0 {
		return liveplay.Score{}, fmt.Errorf("Tymbal returned an invalid zero sample rate")
	}
	config, err := project.CompileEngine(model, rate, engineSoakFrames)
	if err != nil {
		return liveplay.Score{}, err
	}
	config.LoopSong = true
	created, err := engine.New(config)
	if err != nil {
		return liveplay.Score{}, err
	}
	song := make([]liveplay.SongEntry, len(model.Song))
	startBar := uint32(1)
	for index, entry := range model.Song {
		song[index] = liveplay.SongEntry{Scene: entry.Scene, StartBar: startBar}
		startBar += uint32(entry.Bars)
	}
	return liveplay.Score{Engine: created, SampleRate: rate, BPMMilli: int64(model.TempoMilli), Song: song}, nil
}
