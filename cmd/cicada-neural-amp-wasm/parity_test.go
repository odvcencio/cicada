//go:build wasm_integration

package main

import (
	"context"
	"encoding/binary"
	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/amp"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestNeuralAmpWASMBitIdentical(t *testing.T) {
	wasm, err := os.ReadFile(filepath.Join("..", "..", "build", "cicada-neural-amp.wasm"))
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
	call := func(name string, args ...uint64) uint32 {
		t.Helper()
		fn := module.ExportedFunction(name)
		if fn == nil {
			t.Fatalf("missing export %s", name)
		}
		result, err := fn.Call(ctx, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(result) == 0 {
			return 0
		}
		return uint32(result[0])
	}
	call("_initialize")
	ptr := call("cicada_amp_pcm_ptr")
	memory := module.Memory().Size()
	var native amp.Model
	var transfer [128 * 4]byte
	var expected [128]int32
	var state uint32 = 0x12345678
	frames := 0
	for block := 0; block < 512; block++ {
		count := []int{1, 17, 64, 128, 3, 127}[block%6]
		drive := int32(block%9) * 4096
		for i := 0; i < count; i++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			x := int32(state>>16) - 32768
			if block%19 == 0 {
				x = math.MaxInt32
			}
			expected[i] = native.Process(x, drive)
			binary.LittleEndian.PutUint32(transfer[i*4:], uint32(x))
		}
		if !module.Memory().Write(ptr, transfer[:count*4]) || call("cicada_amp_process", uint64(count), uint64(drive)) != 0 {
			t.Fatal("probe transfer/render failed")
		}
		for i := 0; i < count; i++ {
			bits, ok := module.Memory().ReadUint32Le(ptr + uint32(i*4))
			if !ok || int32(bits) != expected[i] {
				t.Fatalf("frame %d: native=%d WASM=%d", frames+i, expected[i], int32(bits))
			}
		}
		frames += count
		if block%71 == 0 {
			native.Reset()
			call("cicada_amp_reset")
		}
	}
	if module.Memory().Size() != memory {
		t.Fatal("WASM render grew memory")
	}
	call("cicada_amp_reset")
	clear(transfer[:])
	if !module.Memory().Write(ptr, transfer[:]) || call("cicada_amp_process", 128, 4096) != 0 {
		t.Fatal("reset transfer failed")
	}
	for i := 0; i < 128; i++ {
		if x, _ := module.Memory().ReadUint32Le(ptr + uint32(i*4)); x != 0 {
			t.Fatal("reset left neural history")
		}
	}
	// The ensemble retains eight independent histories, checked before timing.
	var nativeEnsemble [8]amp.Model
	for block := 0; block < 64; block++ {
		for i := 0; i < 128; i++ {
			x := int32(i*512) - 32768
			var output int32
			for voice := range nativeEnsemble {
				output += nativeEnsemble[voice].Process(x, 8192+int32(voice)*512)
			}
			expected[i] = output >> 3
			binary.LittleEndian.PutUint32(transfer[i*4:], uint32(x))
		}
		if !module.Memory().Write(ptr, transfer[:]) || call("cicada_amp_process_eight", 128, 8192) != 0 {
			t.Fatal("ensemble transfer/render failed")
		}
		for i := 0; i < 128; i++ {
			bits, ok := module.Memory().ReadUint32Le(ptr + uint32(i*4))
			if !ok || int32(bits) != expected[i] {
				t.Fatalf("ensemble frame %d differs: native=%d WASM=%d", block*128+i, expected[i], int32(bits))
			}
		}
	}
	// Fresh nonzero audio enters each block; transfer finishes before timing.
	// Timings include compiled WASM inference and runtime/export overhead.
	for _, profile := range []struct {
		export string
		voices int
	}{
		{"cicada_amp_process", 1}, {"cicada_amp_process_eight", 8},
	} {
		times := make([]time.Duration, 4096)
		for i := range times {
			if !module.Memory().Write(ptr, transfer[:]) {
				t.Fatal("timing input transfer failed")
			}
			start := time.Now()
			call(profile.export, 128, 8192)
			times[i] = time.Since(start)
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		p99 := times[len(times)*99/100]
		if p99 > 670*time.Microsecond {
			t.Fatalf("%d-amp WASM p99 %s exceeds unchanged 670us budget", profile.voices, p99)
		}
		t.Logf("%d simultaneous amps, 128 frames at 48 kHz: WASM p50=%s p99=%s/670us", profile.voices, times[len(times)/2], p99)
	}
	if module.Memory().Size() != memory {
		t.Fatal("ensemble render grew WASM memory")
	}
	t.Logf("%d single-amp Q15 frames and 8192 eight-amp mixed Q15 frames bit-identical; memory=%d bytes with zero growth", frames, memory)
}
