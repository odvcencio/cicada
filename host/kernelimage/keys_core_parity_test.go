//go:build keys_wasm

package kernelimage_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
)

func TestKeysCoreWASMRejectsOptionalImage(t *testing.T) {
	path := os.Getenv("CICADA_KEYS_CORE_WASM")
	if path == "" {
		path = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := keysImageConfig()
	keysImage, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	coreConfig := cfg
	coreConfig.Track[0].Kind, coreConfig.Track[0].Keys = engine.VoiceAcid, nil
	coreImage, err := kernelimage.Encode(coreConfig)
	if err != nil {
		t.Fatal(err)
	}
	unadvertised := append([]byte(nil), keysImage...)
	binary.LittleEndian.PutUint16(unadvertised[30:32], 0)
	for _, tc := range []struct {
		name     string
		image    []byte
		accepted bool
	}{
		{"core", coreImage, true}, {"keys", keysImage, false}, {"unadvertised_keys", unadvertised, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(tc.name))
			if err != nil {
				t.Fatal(err)
			}
			defer module.Close(ctx)
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				values, err := module.ExportedFunction(name).Call(ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(values) > 0 {
					return values[0]
				}
				return 0
			}
			call("_initialize")
			if call("gosx_audio_capabilities")&uint64(kernelimage.KeysCapability) != 0 {
				t.Fatal("core advertises optional keys")
			}
			ptr := uint32(call("gosx_audio_project_alloc", uint64(len(tc.image))))
			if ptr == 0 || !module.Memory().Write(ptr, tc.image) {
				t.Fatal("project buffer unavailable")
			}
			accepted := call("gosx_audio_init", 48000, 128, 2) == 0
			if accepted != tc.accepted {
				t.Fatalf("core accepted=%v expected=%v", accepted, tc.accepted)
			}
		})
	}
}
