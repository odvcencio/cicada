//go:build ddsp_wasm

package ddsp_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/voice/ddsp"
	"m31labs.dev/cicada/kernel/voice/ddsp/testdata/fixture"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestDDSPNativeWASMParityAndBudget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ddsp.wasm")
	build := exec.CommandContext(ctx, "tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./testdata/wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("TinyGo: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)
	m, err := r.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		out, err := m.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 0 {
			return out[0]
		}
		return 0
	}
	call("_initialize")
	ptr := uint32(call("ddsp_fixture_ptr"))
	var native, reference [fixture.Frames]float32
	for index := 0; index < fixture.Cases; index++ {
		fixture.Render(index, 128, reference[:])
		for _, size := range []int{1, 63, 64, 128, 257} {
			if !fixture.Render(index, size, native[:]) || call("ddsp_fixture_render", uint64(index), uint64(size)) != 0 {
				t.Fatal("fixture rejected")
			}
			data, ok := m.Memory().Read(ptr, uint32(len(native)*4))
			if !ok {
				t.Fatal("WASM buffer")
			}
			for i, x := range native {
				bits := binary.LittleEndian.Uint32(data[4*i:])
				if bits != math.Float32bits(x) || x != reference[i] {
					t.Fatalf("parity case=%d size=%d frame=%d native=%08x WASM=%08x", index, size, i, math.Float32bits(x), bits)
				}
			}
		}
	}
	call("ddsp_perf_init")
	initialMemory := m.Memory().Size()
	block := m.ExportedFunction("ddsp_perf_block")
	for range 1000 {
		if _, err := block.Call(ctx, 32); err != nil {
			t.Fatal(err)
		}
	}
	times := make([]time.Duration, 10000)
	for i := range times {
		start := time.Now()
		if _, err := block.Call(ctx, 32); err != nil {
			t.Fatal(err)
		}
		times[i] = time.Since(start)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p99 := times[len(times)*99/100]
	wasmP50 := times[len(times)/2]
	wasmP95 := times[len(times)*95/100]
	var voices [32]ddsp.Synth
	for i := range voices {
		voices[i], _ = ddsp.New(48000)
	}
	var sink int32
	for i := range times {
		start := time.Now()
		for frame := 0; frame < 128; frame++ {
			for j := range voices {
				sink += int32(voices[j].Next(110000+uint32(j)*17000, 26000))
			}
		}
		times[i] = time.Since(start)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	nativeP99 := times[len(times)*99/100]
	if sink == 0 {
		t.Fatal("silent benchmark")
	}
	if m.Memory().Size() != initialMemory {
		t.Fatal("WASM render grew memory")
	}
	fmt.Printf("METRIC DDSP parity_samples=%d bit_exact=true voices=32 frames=128 native_p50_us=%.3f native_p99_us=%.3f wasm_p50_us=%.3f wasm_p95_us=%.3f wasm_p99_us=%.3f budget_us=670 memory_growth=0\n", fixture.Cases*5*fixture.Frames, float64(times[len(times)/2])/1000, float64(nativeP99)/1000, float64(wasmP50)/1000, float64(wasmP95)/1000, float64(p99)/1000)
	if p99 > 670*time.Microsecond || nativeP99 > 670*time.Microsecond {
		t.Fatalf("DDSP p99 exceeds 0.67ms: native=%v wasm=%v", nativeP99, p99)
	}
}
