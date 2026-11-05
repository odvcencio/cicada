package main

import (
	"bytes"
	"math"
	"os"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/patternmachine"
)

func TestMachinePreviewIsAuditionableAndDoesNotSave(t *testing.T) {
	s, source := patternPreviewFixture(t)
	recipe := &patternmachine.Recipe{Version: 1, Seed: 42, Bars: 2, Root: 9, Scale: "minor", Density: 88, Motion: 60}
	request := soundPreviewRequest{Action: "machine", Lease: soundTestLease(t, s), Sequence: 1, Pattern: "generated-idea", Track: "keys", Revision: studioRevision(source), SourceRevision: studioProjectReceipt(t, s.path), Machine: recipe}
	p, _, err := s.machinePreviewProject(s.path, request)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := compileLiveProjectLoopAtRate(s.path, p, 48000, true)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Engine.Close()
	if !prepared.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("preview did not start")
	}
	var left, right [256]float32
	peak := 0.0
	for frame := 0; frame < 48000*2; frame += len(left) {
		prepared.Engine.Render(left[:], right[:])
		for _, value := range left {
			x := math.Abs(float64(value))
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Fatal("non-finite preview")
			}
			peak = max(peak, x)
		}
	}
	if peak < 0.0001 || peak > 1 {
		t.Fatalf("generated preview is silent or clipped: peak=%g", peak)
	}
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 200 {
		t.Fatalf("audition: %d %s", r.Code, r.Body)
	}
	if !s.transport.soundPreviewState(request.Lease).Active {
		t.Fatal("generated preview did not start")
	}
	if after, err := os.ReadFile(s.path); err != nil || !bytes.Equal(after, source) {
		t.Fatal("preview wrote the score")
	}
	request.Sequence++
	request.SourceRevision = "stale-project"
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 409 {
		t.Fatalf("stale project audition: %d %s", r.Code, r.Body)
	}
	request.SourceRevision = studioProjectReceipt(t, s.path)
	request.Sequence++
	request.Revision = "stale"
	if r := studioCall(t, s.routes(), "/api/sound-preview", request); r.Code != 409 {
		t.Fatalf("stale audition: %d %s", r.Code, r.Body)
	}
}
