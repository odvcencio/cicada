package liveplay

import (
	"math"
	"runtime"
	"testing"
	"time"
)

func TestHostLoudnessMatchesBS1770StereoOneKilohertzReference(t *testing.T) {
	meter, err := newLiveLoudness(48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer meter.close()

	const sampleRate = 48_000
	const totalFrames = sampleRate * 20
	// BS.1770-4 gives each stereo channel weight 1 and applies the -0.691 LU
	// calibration. At 1 kHz this kernel's combined K-weighting response is
	// +0.698 dB. With A=10^(-23/20), the summed channel mean-square is A², so
	// -0.691 + 10log10(A²) + 0.698 = -22.993 LUFS, rounding to -23.0 LUFS
	// (also EBU Tech 3341 v4, Table 1, case 1).
	amplitude := float32(math.Pow(10, -23.0/20))
	var left, right [blockFrames]float32
	phase := 0
	for sent := 0; sent < totalFrames; {
		frames := min(blockFrames, totalFrames-sent)
		for frame := 0; frame < frames; frame++ {
			sample := amplitude * float32(math.Sin(2*math.Pi*1000*float64(phase)/sampleRate))
			left[frame], right[frame] = sample, sample
			phase = (phase + 1) % sampleRate
		}
		for meter.ring.write.Load()-meter.ring.read.Load() >= loudnessBlockCount-2 {
			runtime.Gosched()
		}
		if !meter.ring.push(left[:frames], right[:frames]) {
			t.Fatal("reference block was unexpectedly dropped")
		}
		sent += frames
	}
	deadline := time.Now().Add(5 * time.Second)
	for meter.ring.read.Load() != meter.ring.write.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if meter.ring.read.Load() != meter.ring.write.Load() {
		t.Fatal("meter worker did not drain the reference tone")
	}
	var result LoudnessSnapshot
	for time.Now().Before(deadline) {
		result = meter.metrics()
		if result.HasIntegrated {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !result.HasIntegrated || math.Abs(result.IntegratedLUFS-(-23.0)) > 0.1 {
		t.Fatalf("integrated loudness: %.3f LUFS (valid=%t), want -23.0 ±0.1 LUFS", result.IntegratedLUFS, result.HasIntegrated)
	}
	t.Logf("post-limiter host path, 20 s stereo 1 kHz at -23 dBFS/channel: integrated %.4f LUFS (deviation %+.4f LU)", result.IntegratedLUFS, result.IntegratedLUFS+23)
	if result.DroppedBlocks != 0 {
		t.Fatalf("reference tone dropped %d blocks", result.DroppedBlocks)
	}
}

func TestLoudnessRingDropsWholeBlocksWithoutBlockingOrAllocating(t *testing.T) {
	var ring loudnessRing
	var left, right [blockFrames]float32
	left[0], right[0] = .25, -.25
	for block := 0; block < loudnessBlockCount; block++ {
		if !ring.push(left[:], right[:]) {
			t.Fatalf("ring rejected block %d before reaching capacity", block)
		}
	}
	finished := make(chan bool, 1)
	started := time.Now()
	go func() { finished <- ring.push(left[:], right[:]) }()
	select {
	case accepted := <-finished:
		if accepted {
			t.Fatal("overflow block was partially or fully accepted")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("audio path blocked while the meter ring was full")
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("full-ring push took %s", elapsed)
	}
	if got := ring.dropped.Load(); got != 1 {
		t.Fatalf("dropped block count=%d, want 1", got)
	}
	if got := ring.write.Load(); got != loudnessBlockCount {
		t.Fatalf("ring advanced by a partial overflow block: write=%d", got)
	}
	if got := testing.AllocsPerRun(100, func() {
		ring.read.Store(ring.write.Load())
		if !ring.push(left[:], right[:]) {
			panic("empty ring rejected block")
		}
	}); got != 0 {
		t.Fatalf("audio ring push allocated %.2f objects", got)
	}
}

func TestLoudnessResetClearsIntegratedAndRangeOnWorker(t *testing.T) {
	meter, err := newLiveLoudness(48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer meter.close()
	var left, right [blockFrames]float32
	for frame := range left {
		left[frame], right[frame] = .1, .1
	}
	for block := 0; block < 1200; block++ {
		for meter.ring.write.Load()-meter.ring.read.Load() >= loudnessBlockCount-2 {
			runtime.Gosched()
		}
		if !meter.ring.push(left[:], right[:]) {
			t.Fatal("unexpected block drop while preparing reset")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && meter.ring.read.Load() != meter.ring.write.Load() {
		time.Sleep(time.Millisecond)
	}
	for time.Now().Before(deadline) {
		result := meter.metrics()
		if result.HasIntegrated && result.HasRange {
			break
		}
		time.Sleep(time.Millisecond)
	}
	beforeReset := meter.metrics()
	if !beforeReset.HasIntegrated || !beforeReset.HasRange {
		t.Fatalf("integrated loudness/range did not become available before reset: integrated=%t range=%t", beforeReset.HasIntegrated, beforeReset.HasRange)
	}
	meter.resetMeter()
	for time.Now().Before(deadline) {
		result := meter.metrics()
		if !result.HasIntegrated && !result.HasRange {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("reset request did not clear integrated loudness and range")
}
