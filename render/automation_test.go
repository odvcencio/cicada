package render

import (
	"bytes"
	"io"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/project"
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestContinuousAutomationWAVMatchesPlayback(t *testing.T) {
	source, err := os.ReadFile("../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	var output bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 4, Block: 128, TailSec: .1}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak < .001 {
		t.Fatal("automation rendered silence")
	}
	assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48000, 0, 388800)
}

func TestOfflineAutomationRenderAllocationFree(t *testing.T) {
	source, err := os.ReadFile("../examples/continuous-automation.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, _ := notation.Parse(source)
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	tracks, err := compileTracks(score, p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := compileSceneParameters(p, tracks, 48000, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	tracks[0].automation = parameters
	limiter, err := mix.NewLimiter(48000)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := newWAVEncoder(32, false, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var buffer [1024]byte
	report := Report{Frames: 128}
	if n := testing.AllocsPerRun(100, func() {
		parameters.automationIndex = 0
		limiter.Reset()
		report.writtenFrames = 0
		if err := renderBlock(io.Discard, tracks, nil, nil, nil, 0, 0, 0, 1, busMixerState{outputGain: 1}, limiter, nil, &encoder, nil, 0, 128, buffer[:], &report); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("offline automation render allocations %g", n)
	}
}
