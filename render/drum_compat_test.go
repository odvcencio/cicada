package render_test

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestLegacyThreeDrumTracksValidateAndRender(t *testing.T) {
	source, err := os.ReadFile("../testdata/compat/three-drums.cicada")
	if err != nil {
		t.Fatal(err)
	}
	var firstWAV bytes.Buffer
	for pass := 0; pass < 2; pass++ {
		score, diagnostics := notation.Parse(source)
		if len(diagnostics) != 0 {
			t.Fatalf("parse: %+v", diagnostics)
		}
		p, diagnostics := project.FromScore(score)
		if p == nil || len(diagnostics) != 0 {
			t.Fatalf("validate: %+v", diagnostics)
		}
		cfg, err := project.CompileEngine(p, 48_000, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxVoices != 32 {
			t.Fatalf("realtime cap changed to %d", cfg.MaxVoices)
		}
		cfg.MaxVoices = 17
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("three legacy kits must reserve 18 voices")
		}
		cfg.MaxVoices = 18
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := kernelimage.Decode(image, 48_000, 128)
		if err != nil || !reflect.DeepEqual(cfg, decoded) {
			t.Fatalf("image round trip changed the configuration: %v", err)
		}
		e, err := engine.New(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("play command rejected")
		}
		var left, right [128]float32
		var heard [3]bool
		var energy float64
		for range 128 {
			e.Render(left[:], right[:])
			for _, sample := range left {
				energy += float64(sample * sample)
			}
			var message cmd.Message
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("native fault: %+v", message)
				}
				if message.Kind == cmd.NoteOn && message.Track < 3 {
					heard[message.Track] = true
				}
			}
		}
		if energy == 0 || heard != [3]bool{true, true, true} {
			t.Fatalf("native render: energy=%g heard=%v", energy, heard)
		}
		var wav bytes.Buffer
		report, err := render.WAV(score, render.Options{SampleRate: 48_000, Bars: 1}, &wav)
		if err != nil || report.Peak == 0 || report.Frames == 0 {
			t.Fatalf("offline render: %+v, %v", report, err)
		}
		if pass == 0 {
			firstWAV.Write(wav.Bytes())
		} else if !bytes.Equal(firstWAV.Bytes(), wav.Bytes()) {
			t.Fatal("source round trip changed the audio")
		}
		source, err = project.ToSource(p)
		if err != nil {
			t.Fatal(err)
		}
	}
}
