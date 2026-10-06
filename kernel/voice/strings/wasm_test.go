//go:build keys_wasm

package strings_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/voice/strings/testdata/fixture"
)

func TestNativeWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_STRINGS_FIXTURE_WASM")
	if path == "" {
		path = filepath.Join(t.TempDir(), "strings.wasm")
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
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	m, err := r.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		values, err := m.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(values) > 0 {
			return values[0]
		}
		return 0
	}
	call("_initialize")
	ptr := call("keys_fixture_ptr")
	native := make([]float32, fixture.Frames*2)
	for index := 0; index < fixture.Cases; index++ {
		for _, block := range []int{64, 128, 256} {
			if err := fixture.Render(index, block, native); err != nil {
				t.Fatal(err)
			}
			if int32(call("keys_fixture_render", uint64(index), uint64(block))) != 0 {
				t.Fatal("fixture rejected")
			}
			pcm, ok := m.Memory().Read(uint32(ptr), uint32(len(native)*4))
			if !ok {
				t.Fatal("PCM outside memory")
			}
			for n, value := range native {
				bits := binary.LittleEndian.Uint32(pcm[n*4:])
				if bits != math.Float32bits(value) {
					t.Fatalf("case=%d block=%d sample=%d native=%08x wasm=%08x", index, block, n, math.Float32bits(value), bits)
				}
			}
		}
	}
	call("keys_alloc_init")
	before := call("keys_alloc_bytes")
	for range 100 {
		call("keys_alloc_render")
	}
	if after := call("keys_alloc_bytes"); after != before {
		t.Fatalf("WASM trigger/render allocation delta=%d", after-before)
	}
	t.Logf("rates=44100,48000,96000,192000 blocks=64,128,256 samples=%d bit_exact=true render_allocations=0 fixture_bytes=%d", fixture.Cases*3*len(native), len(wasm))
}
