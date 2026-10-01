//go:build sample_wasm

package sample_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/voice/sample/testdata/fixture"
)

// Run under local-heavy.lock: this builds the fixture with the same TinyGo
// flags as the kernel, then verifies every PCM bit against native Go at each
// block size. No engine import, integration change or committed binary is used.
func TestSampleNativeWASMDeterminism(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sample.wasm")
	build := exec.Command("tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./testdata/wasm")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
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
		t.Fatalf("initialize fixture: %v", err)
	}
	ptr, err := module.ExportedFunction("sample_fixture_ptr").Call(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var native [fixture.Frames * 2]float32
	for index := 0; index < fixture.Cases; index++ {
		for _, size := range []int{64, 128, 256} {
			if err := fixture.Render(index, size, native[:]); err != nil {
				t.Fatal(err)
			}
			result, err := module.ExportedFunction("sample_fixture_render").Call(ctx, uint64(index), uint64(size))
			if err != nil || len(result) != 1 || int32(result[0]) != 0 {
				t.Fatalf("render: %v, %v", result, err)
			}
			data, ok := module.Memory().Read(uint32(ptr[0]), uint32(len(native)*4))
			if !ok {
				t.Fatal("PCM is outside WASM memory")
			}
			for i, value := range native {
				bits := binary.LittleEndian.Uint32(data[i*4:])
				if bits != math.Float32bits(value) {
					t.Fatalf("case=%d block=%d sample=%d: native=%08x wasm=%08x delta=%g", index, size, i, math.Float32bits(value), bits, math.Abs(float64(value)-float64(math.Float32frombits(bits))))
				}
			}
		}
	}
	fmt.Printf("METRIC PARITY compiler=TinyGo cases=%d block_sizes=64,128,256 samples=%d bit_exact=true max_abs_difference=0\n", fixture.Cases, fixture.Cases*3*len(native))
}
