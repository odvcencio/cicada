package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/render"
)

func TestSyntheticSessions(t *testing.T) {
	if got := len(scenarios()); got != 640 {
		t.Fatalf("matrix has %d rows, want 640", got)
	}
	for _, s := range scenarios() {
		if s.sampler() {
			continue
		}
		_, cfg, err := s.score()
		if err != nil {
			t.Fatalf("%s: %v", s.key(), err)
		}
		if cfg.Tracks != s.tracks || cfg.Seed != seed {
			t.Fatalf("wrong config: %s", s.key())
		}
		if _, err := newNative(s, cfg); err != nil {
			t.Fatalf("%s: %v", s.key(), err)
		}
	}
}

func TestMetricsRejectShortSceneRun(t *testing.T) {
	o := options{blocks: 750, runs: 3, warmup: 128, bars: 2, filter: "rate_hz=48000,block_frames=128"}
	if o.validate() == nil {
		t.Fatal("accepted a timed run that does not cross a bar")
	}
	o.blocks++
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	if got := nearestRank([]int64{1, 2, 3}, 50); got != 2 {
		t.Fatalf("median = %d", got)
	}
	if got := nearestRank([]int64{1, 2, 3}, 99); got != 3 {
		t.Fatalf("p99 = %d", got)
	}
}

// Exercise the actual clocked loop, including the launch at a bar boundary,
// with the most expensive effects enabled and the maximum track count.
func TestTimedHarnessAllocationFree(t *testing.T) {
	for _, kind := range []string{"acid", "drums", "graph", "sampler-1", "sampler-1.5"} {
		t.Run(kind, func(t *testing.T) {
			s := scenario{kind: kind, tracks: 16, rate: 48000, block: 128, drive: true, sends: true, scenes: true}
			o := options{blocks: 751, runs: 1, warmup: 1, bars: 2}
			var output bytes.Buffer
			if err := measure(s, o, &output); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(output.Bytes(), []byte("allocs=0 bytes=0")) || !bytes.Contains(output.Bytes(), []byte("scene_changes=1")) {
				t.Fatalf("allocation or scene metric missing: %s", output.Bytes())
			}
		})
	}
}

// The same generated scores drive the live and offline engines. Compare every
// PCM sample through the second scene, using the repository's 1e-6 tolerance.
func TestSyntheticOfflineParity(t *testing.T) {
	for _, s := range scenarios() {
		if s.sampler() || s.tracks != 1 {
			continue
		}
		t.Run(s.key(), func(t *testing.T) {
			_, _, _ = offlineReference(t, s)
		})
	}
}

// offlineReference also supplies latency-aligned PCM to the WASM gate.
func offlineReference(t *testing.T, s scenario) ([]float32, []float32, int) {
	t.Helper()
	score, cfg, err := s.score()
	if err != nil {
		t.Fatal(err)
	}
	var wav bytes.Buffer
	report, err := render.WAV(score, render.Options{SampleRate: s.rate, Block: s.block, Bits: 32, Bars: 2}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	n, err := newNative(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := mix.NewLimiter(s.rate)
	if err != nil {
		t.Fatal(err)
	}
	latency := limiter.LatencyFrames()
	if s.drive {
		latency += fx.DriveLatencyFrames
	}
	frames := int(report.Frames)
	pcmL, pcmR := make([]float32, frames), make([]float32, frames)
	data := wav.Bytes()[44 : 44+frames*8]
	for i := 0; i < frames; i++ {
		pcmL[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8:]))
		pcmR[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*8+4:]))
	}
	var left, right [256]float32
	var difference float64
	for position := 0; position < frames+latency; position += s.block {
		n.Render(left[:s.block], right[:s.block])
		for i := 0; i < s.block; i++ {
			frame := position + i - latency
			if frame < 0 || frame >= frames {
				continue
			}
			difference = max(difference, math.Abs(float64(left[i])-float64(pcmL[frame])), math.Abs(float64(right[i])-float64(pcmR[frame])))
		}
	}
	if n.fault || math.IsNaN(difference) || difference > 1e-6 {
		t.Fatalf("native/offline difference %g, fault=%t", difference, n.fault)
	}
	return pcmL, pcmR, latency
}
