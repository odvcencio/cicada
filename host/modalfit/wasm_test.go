//go:build wasm_integration

package modalfit

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
)

func TestFittedModalNativeWASMParity(t *testing.T) {
	model := recordedModel(t)
	path := filepath.Join(t.TempDir(), "modal.wasm")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./kernel/voice/modal/testdata/fitted-wasm")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-buildvcs=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fitted modal fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	m, err := runtime.Instantiate(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		r, err := m.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(name, err)
		}
		if len(r) > 0 {
			return r[0]
		}
		return 0
	}
	call("_initialize")
	params := uint32(call("parameters_ptr"))
	m.Memory().WriteFloat64Le(params, model.NoiseMix)
	for i, mode := range model.Modes {
		for j, x := range []float64{mode.Ratio, mode.Weight, mode.T60} {
			m.Memory().WriteFloat64Le(params+uint32(8*(1+i*3+j)), x)
		}
	}
	if call("prepare", uint64(len(model.Modes))) != 0 {
		t.Fatal("modal prepare")
	}
	v, err := model.Voice(48000)
	if err != nil {
		t.Fatal(err)
	}
	out := uint32(call("output_ptr"))
	memory := m.Memory().Size()
	maximum := 0.0
	for block := 0; block < 512; block++ {
		if block%32 == 0 {
			note, velocity := uint8([]int{36, 60, 72, 108}[block/32%4]), uint8([]int{25, 80, 127}[block/32%3])
			slide := block%96 == 32
			v.NoteOn(note, velocity, slide)
			flag := uint64(0)
			if slide {
				flag = 1
			}
			call("note_on", uint64(note), uint64(velocity), flag)
		}
		call("render")
		for i := 0; i < 128; i++ {
			bits, ok := m.Memory().ReadUint32Le(out + uint32(i*4))
			if !ok {
				t.Fatal("WASM output bounds")
			}
			want, got := v.Next(), math.Float32frombits(bits)
			delta := math.Abs(float64(want) - float64(got))
			if math.IsNaN(delta) || delta > 1e-6 {
				t.Fatalf("fitted modal parity block %d sample %d: %g vs %g", block, i, want, got)
			}
			maximum = max(maximum, delta)
		}
	}
	if m.Memory().Size() != memory {
		t.Fatal("fitted modal render grew WASM memory")
	}
	t.Logf("65536 fitted-modal samples, dynamics, overlap, glide and octave changes; max difference %.9g; no memory growth", maximum)
}
