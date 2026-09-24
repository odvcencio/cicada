package mix

import (
	"math"
	"testing"
)

func TestDryTrackPanAndMusicHeadroom(t *testing.T) {
	var dry Dry
	dry.Add(1, 1, NewTrack(-6, -1, false))
	left, right := dry.Music()
	want := float32(math.Pow(10, -9.0/20))
	if math.Abs(float64(left-want)) > 1e-6 || math.Abs(float64(right)) > 1e-6 {
		t.Fatalf("left pan: %f %f", left, right)
	}
	var muted Dry
	muted.Add(1, 1, NewTrack(6, 0, true))
	l, r := muted.Music()
	if l != 0 || r != 0 {
		t.Fatal("off fader produced output")
	}
}

func TestLimiterLinkedLookaheadAndRelease(t *testing.T) {
	limiter, err := NewLimiter(48_000)
	if err != nil {
		t.Fatal(err)
	}
	latency := limiter.LatencyFrames()
	if latency != 72 {
		t.Fatalf("latency %d", latency)
	}
	input := make([]float32, latency+100)
	for i := range input {
		input[i] = .25
	}
	input[latency] = 4
	var output []float32
	for _, sample := range input {
		left, right, ready := limiter.Process(sample, sample*.5)
		if ready {
			if math.Abs(float64(left)) > limiter.Ceiling()+1e-6 || math.Abs(float64(right)) > limiter.Ceiling()+1e-6 {
				t.Fatal("limiter exceeded ceiling")
			}
			output = append(output, left)
		}
	}
	for i := 0; i < latency; i++ {
		left, _, ready := limiter.Process(0, 0)
		if !ready {
			t.Fatal("flush stalled")
		}
		output = append(output, left)
	}
	if len(output) != len(input) {
		t.Fatalf("output length %d", len(output))
	}
	if output[0] >= .25 || output[latency] > .967 {
		t.Fatalf("lookahead did not anticipate impulse: %f %f", output[0], output[latency])
	}
	if limiter.Fault() {
		t.Fatal("limiter faulted")
	}
}

func TestLimiterSampleDoesNotAllocate(t *testing.T) {
	limiter, _ := NewLimiter(48_000)
	allocs := testing.AllocsPerRun(1000, func() { limiter.Process(.2, -.2) })
	if allocs != 0 {
		t.Fatalf("limiter allocated %.2f objects per sample", allocs)
	}
}

func BenchmarkLimiter(b *testing.B) {
	limiter, _ := NewLimiter(48_000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		limiter.Process(.75, -.5)
	}
}
