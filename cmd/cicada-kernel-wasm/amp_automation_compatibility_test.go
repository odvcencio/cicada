//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMAmpAutomationCompatibility(t *testing.T) {
	corePath, trackPath := os.Getenv("CICADA_CORE_WASM"), os.Getenv("CICADA_VOCALS_WASM")
	if corePath == "" {
		corePath = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	if trackPath == "" {
		trackPath = filepath.Join("..", "..", "build", "cicada-vocals.wasm")
	}
	score, _ := notation.Parse([]byte(`cicada 2
fx rig ampcab { drive = 0.6 }
track lead acid { insert = rig }
pattern line acid { 1 . 3 . }
scene main { lead = line }
song { main }
automate lead.amp_drive { @1.1.1 0.2 @1.2.1 0.8 smooth }
`))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	modern, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	unadvertised := append([]byte(nil), modern...)
	binary.LittleEndian.PutUint16(unadvertised[30:32], binary.LittleEndian.Uint16(unadvertised[30:32])&^kernelimage.AmpAutomationCapability)
	cfg.Automation = nil
	legacy, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint16(legacy[30:32], binary.LittleEndian.Uint16(legacy[30:32])&^kernelimage.AmpAutomationCapability)
	for _, tc := range []struct {
		name, path        string
		image             []byte
		accepted, capable bool
	}{
		{"core-rejects-new", corePath, modern, false, false},
		{"trackfx-accepts-new", trackPath, modern, true, true},
		{"trackfx-rejects-unadvertised", trackPath, unadvertised, false, true},
		{"trackfx-accepts-legacy", trackPath, legacy, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			runtime := wazero.NewRuntime(ctx)
			defer runtime.Close(ctx)
			mod, err := runtime.Instantiate(ctx, data)
			if err != nil {
				t.Fatal(err)
			}
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				values, err := mod.ExportedFunction(name).Call(ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(values) > 0 {
					return values[0]
				}
				return 0
			}
			call("_initialize")
			if got := call("gosx_audio_capabilities")&uint64(kernelimage.AmpAutomationCapability) != 0; got != tc.capable {
				t.Fatalf("capability = %v", got)
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(tc.image))))
			if ptr == 0 || !mod.Memory().Write(ptr, tc.image) {
				t.Fatal("image upload failed")
			}
			if got := call("gosx_audio_init", 48000, 128, 2) == 0; got != tc.accepted {
				t.Fatalf("accepted = %v, want %v", got, tc.accepted)
			}
		})
	}
}
