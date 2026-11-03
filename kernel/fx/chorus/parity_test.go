//go:build wasm_integration

package chorus

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
)

// TestWASMParity compares the native processor to the separately compiled
// TinyGo reactor. Run with CICADA_CHORUS_WASM pointing to testdata/probe.
func TestWASMParity(t *testing.T) {
	path := os.Getenv("CICADA_CHORUS_WASM")
	if path == "" {
		t.Skip("set CICADA_CHORUS_WASM to the compiled parity probe")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	module, err := runtime.CompileModule(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		for voices := 1; voices <= 3; voices++ {
			instance, err := runtime.InstantiateModule(ctx, module, wazero.NewModuleConfig().WithName(""))
			if err != nil {
				t.Fatal(err)
			}
			call := func(name string, args ...uint64) uint64 {
				t.Helper()
				f := instance.ExportedFunction(name)
				if f == nil {
					t.Fatalf("missing export %s", name)
				}
				r, err := f.Call(ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				if len(r) == 0 {
					return 0
				}
				return r[0]
			}
			if instance.ExportedFunction("_initialize") != nil {
				call("_initialize")
			}
			if call("probe_init", uint64(rate), uint64(voices)) != 0 {
				t.Fatal("probe init failed")
			}
			params := DefaultParams()
			params.Voices, params.Rate, params.Depth, params.Spread, params.Mix = voices, 1.7, 5, .4, .7
			native, err := New(rate, params)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10000; i++ {
				l := float32(int(uint32(i)%511)-255) / 1024
				r := float32(int((uint32(i)*37)%503)-251) / 1024
				left, right := native.Process(l, r)
				got := call("probe_step", uint64(i))
				want := uint64(math.Float32bits(left)) | uint64(math.Float32bits(right))<<32
				if got != want {
					t.Fatalf("rate %d voices %d sample %d: wasm %x native %x", rate, voices, i, got, want)
				}
			}
			if err := instance.Close(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
}
