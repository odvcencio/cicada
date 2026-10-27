//go:build !race

package main

import (
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/ampcab"
)

// No player worker exists in this callback-only measurement. Real player
// automation and its no-allocation accessor are checked in host/liveplay.
type allocationAmpSource struct{}

func (*allocationAmpSource) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func (*allocationAmpSource) MonitorAmpTargets(liveplay.PreviewTarget) (engine.AmpTargets, bool) {
	return engine.AmpTargets{Drive: .1, Tone: .8, Output: -3, Cabinet: 1}, true
}

func TestPreparedAmpFirstCallbackDoesNotAllocate(t *testing.T) {
	source := &allocationAmpSource{}
	const iterations = 10
	devices := make([]*backendStudioAudio, iterations+1)
	for i := range devices {
		a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
		a.SetSource(source)
		a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "stereo", AmpDrive: .35, AmpTone: .5})
		if err := a.SetAmpFollower(&studioAmpFollower{player: source, initial: ampcab.DefaultParams()}); err != nil {
			t.Fatal(err)
		}
		a.playing.Store(true)
		devices[i] = a
	}
	input, output := [][]float32{make([]float32, 128), make([]float32, 128)}, [][]float32{make([]float32, 128), make([]float32, 128)}
	i := 0
	allocs := testing.AllocsPerRun(iterations, func() { a := devices[i]; i++; _ = a.renderPeriod(input, output) })
	if allocs != 0 {
		t.Fatalf("first bound callback allocated %g times", allocs)
	}
	if allocs := testing.AllocsPerRun(20, func() { _ = devices[0].renderPeriod(input, output) }); allocs != 0 {
		t.Fatalf("steady bound callback allocated %g times", allocs)
	}
}
