package wam2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestWAM2Export(t *testing.T) {
	source, err := os.ReadFile("../../examples/live-intensity.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	dir := t.TempDir()
	sdk := filepath.Join(dir, "sdk")
	for _, name := range []string{"dist/index.js", "src/RingBuffer_LICENSE.txt"} {
		path := filepath.Join(sdk, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	kernel := filepath.Join(dir, "kernel.wasm")
	if err := os.WriteFile(kernel, []byte("\x00asm\x01\x00\x00\x00"), 0644); err != nil {
		t.Fatal(err)
	}
	o := Options{KernelPath: kernel, SDKPath: sdk, MIDITrack: "bass"}
	output := filepath.Join(dir, "plugin")
	if err := Export(p, ".", output, o); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "score.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.MIDITrack != 0 || len(m.Macros) != 1 || m.Macros[0].ID != "intensity" || m.Macros[0].Default != .3 || m.Macros[0].SmoothMS != 400 {
		t.Fatalf("incorrect manifest: %+v", m)
	}
	if len(m.DrumNotes) < 1 || m.DrumNotes[0] != 36 {
		t.Fatal("incorrect MIDI drum map")
	}
	for _, rate := range []int{44100, 48000, 96000} {
		var key string
		switch rate {
		case 44100:
			key = "44100"
		case 48000:
			key = "48000"
		case 96000:
			key = "96000"
		}
		image, err := os.ReadFile(filepath.Join(output, m.Images[key]))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := kernelimage.Decode(image, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Tracks != len(p.Tracks) {
			t.Fatal("lost tracks")
		}
		setup := m.Setup[key]
		define, err := cmd.DecodeCommand(setup[:24], uint8(cfg.Tracks))
		if err != nil || define.Op != cmd.OpDefineMacro || define.Index != 0 {
			t.Fatalf("invalid macro setup: %+v %v", define, err)
		}
	}
	if err := Export(p, ".", output, o); err == nil {
		t.Fatal("overwrote output")
	}
	o.MIDITrack = "missing"
	bad := filepath.Join(dir, "invalid")
	if err := Export(p, ".", bad, o); err == nil {
		t.Fatal("accepted unknown MIDI track")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("published incomplete plugin")
	}
	for _, name := range []string{"index.js", "processor.js", "gui.js", "host.html", "sdk.js", "descriptor.json", "kernel.wasm", "RingBuffer_LICENSE.txt"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatal(err)
		}
	}
}
