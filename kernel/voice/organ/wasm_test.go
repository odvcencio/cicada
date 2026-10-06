//go:build organ_wasm

package organ_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/voice/organ/testdata/fixture"
)

func TestOrganNativeWASMDeterminism(t *testing.T) {
	path := os.Getenv("CICADA_ORGAN_FIXTURE_WASM")
	if path == "" {
		path = filepath.Join(t.TempDir(), "organ.wasm")
		build := exec.Command("tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./testdata/wasm")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, output)
		}
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
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
	ptr := call("organ_fixture_ptr")
	native := make([]float32, fixture.Frames*2)
	for index := 0; index < fixture.Cases; index++ {
		for _, size := range []int{64, 128, 256} {
			if err := fixture.Render(index, size, native); err != nil {
				t.Fatal(err)
			}
			if int32(call("organ_fixture_render", uint64(index), uint64(size))) != 0 {
				t.Fatal("render rejected")
			}
			data, ok := module.Memory().Read(uint32(ptr), uint32(len(native)*4))
			if !ok {
				t.Fatal("PCM outside memory")
			}
			for i, value := range native {
				bits := binary.LittleEndian.Uint32(data[i*4:])
				if bits != math.Float32bits(value) {
					t.Fatalf("rate_case=%d block=%d sample=%d native=%08x wasm=%08x", index, size, i, math.Float32bits(value), bits)
				}
			}
		}
	}
	call("organ_bench_init", 8)
	before := call("organ_bench_alloc")
	for range 100 {
		call("organ_bench_strike")
		call("organ_bench_render", 128)
	}
	if after := call("organ_bench_alloc"); after != before {
		t.Fatalf("WASM note/render allocation delta=%d", after-before)
	}
	t.Logf("rates=44100,48000,96000,192000 blocks=64,128,256 samples=%d bit_exact=true render_allocations=0", fixture.Cases*3*len(native))
}
