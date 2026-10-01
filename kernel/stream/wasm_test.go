//go:build stream_wasm

package stream_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/stream/testdata/fixture"
)

func TestStreamNativeWASMParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "stream.wasm")
	build := exec.CommandContext(ctx, "tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./testdata/wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("stream WASM build: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.ExportedFunction("_initialize").Call(ctx); err != nil {
		t.Fatal(err)
	}
	ptr, err := module.ExportedFunction("stream_fixture_ptr").Call(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var native [fixture.Frames * 2]float32
	for _, size := range []int{64, 128, 256} {
		if err := fixture.Render(size, native[:]); err != nil {
			t.Fatal(err)
		}
		result, err := module.ExportedFunction("stream_fixture_render").Call(ctx, uint64(size))
		if err != nil || len(result) != 1 || int32(result[0]) != 0 {
			t.Fatalf("render: %v %v", result, err)
		}
		data, ok := module.Memory().Read(uint32(ptr[0]), uint32(len(native)*4))
		if !ok {
			t.Fatal("stream PCM outside WASM memory")
		}
		for i, value := range native {
			if binary.LittleEndian.Uint32(data[i*4:]) != math.Float32bits(value) {
				t.Fatalf("block=%d sample=%d native=%g wasm=%g", size, i, value, math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:])))
			}
		}
	}
	t.Log("stream native/TinyGo parity: 98304 samples, blocks=64/128/256, peak difference=0")
}
