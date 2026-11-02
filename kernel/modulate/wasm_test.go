//go:build modulate_wasm

package modulate_test

import (
	"context"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/modulate/testdata/fixture"
	"os"
)

// TestModulateNativeWASMParity builds the fixture with TinyGo, the compiler of
// the browser kernel, and requires the same digest as the native build.
func TestModulateNativeWASMParity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "modulate.wasm")
	build := exec.CommandContext(ctx, "tinygo", "build", "-target=wasm-unknown", "-opt=2", "-panic=trap", "-no-debug", "-gc=leaking", "-scheduler=none", "-o", path, "./testdata/wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("modulate WASM build: %v\n%s", err, out)
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
	if _, err := module.ExportedFunction("modulate_fixture_run").Call(ctx); err != nil {
		t.Fatal(err)
	}
	ptr, err := module.ExportedFunction("modulate_fixture_ptr").Call(ctx)
	if err != nil {
		t.Fatal(err)
	}
	data, ok := module.Memory().Read(uint32(ptr[0]), 32)
	if !ok {
		t.Fatal("digest outside WASM memory")
	}
	native := fixture.Digest()
	if got := hex.EncodeToString(data); got != hex.EncodeToString(native[:]) || got != fixture.ExpectedDigest {
		t.Fatalf("wasm digest %s, native %x, expected %s", got, native, fixture.ExpectedDigest)
	}
	t.Logf("modulate native/TinyGo parity: digest %s", fixture.ExpectedDigest)
}
