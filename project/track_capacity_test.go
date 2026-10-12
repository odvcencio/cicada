//go:build !tinygo && !js

package project_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func capacitySource(n int) []byte {
	var s strings.Builder
	s.WriteString("cicada 2\ntempo 120\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&s, "track t%d acid {level=-42db}\n", i)
	}
	s.WriteString("pattern p acid steps=1 {1}\nscene main {\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&s, "t%d=p\n", i)
	}
	s.WriteString("}\nsong {main}\n")
	return []byte(s.String())
}

func TestNativeSixtyFourTracksCompileRenderAndPortableRejects(t *testing.T) {
	score, ds := notation.Parse(capacitySource(64))
	p, ds2 := project.FromScore(score)
	if p == nil || len(ds) != 0 || len(ds2) != 0 {
		t.Fatalf("64-track source: %v %v", ds, ds2)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil || cfg.Tracks != 64 || cfg.MaxVoices != 64 || cfg.Track[63].Kind != engine.VoiceAcid || cfg.Scenes[0].Track[63].Mode != engine.SceneSlot {
		t.Fatalf("64-track config: tracks=%d voices=%d %v", cfg.Tracks, cfg.MaxVoices, err)
	}
	address, err := project.ResolveParameterPath(p, "t63.pan")
	if err != nil || address.Track != 63 {
		t.Fatalf("last track parameter: %+v %v", address, err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	var left, right [128]float32
	peak := float64(0)
	for block := 0; block < 20; block++ {
		e.Render(left[:], right[:])
		for _, v := range left {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatal("nonfinite PCM")
			}
			peak = max(peak, math.Abs(float64(v)))
		}
	}
	if peak == 0 {
		t.Fatal("64-track render silent")
	}
	if image, err := kernelimage.Encode(cfg); err == nil || image != nil || !strings.Contains(err.Error(), "16 tracks and 32") {
		t.Fatalf("native project portable export: %d bytes %v", len(image), err)
	}
}

func TestNativeTrackOverflowHasSourceDiagnostic(t *testing.T) {
	_, ds := notation.Parse(capacitySource(65))
	for _, d := range ds {
		if d.Code == "CICADA-LIMIT" && strings.Contains(d.Message, "64 tracks") {
			return
		}
	}
	t.Fatalf("65 tracks accepted: %v", ds)
}
