package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStudioKernelEndpointSelectsOptionalKeysModule(t *testing.T) {
	directory := t.TempDir()
	core, keys := filepath.Join(directory, "core.wasm"), filepath.Join(directory, "keys.wasm")
	for path, data := range map[string]string{core: "core-module", keys: "keys-module"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CICADA_KERNEL_WASM", core)
	t.Setenv("CICADA_KEYS_WASM", keys)
	for _, tc := range []struct{ url, data string }{
		{"/api/kernel.wasm", "core-module"}, {"/api/kernel.wasm?keys=0", "core-module"}, {"/api/kernel.wasm?keys=1", "keys-module"},
	} {
		response := httptest.NewRecorder()
		new(studio).kernelWASM(response, httptest.NewRequest("GET", tc.url, nil))
		if response.Code != 200 || response.Body.String() != tc.data || response.Header().Get("Content-Type") != "application/wasm" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("url=%s response=%d data=%q headers=%v", tc.url, response.Code, response.Body.String(), response.Header())
		}
	}
	t.Setenv("CICADA_KEYS_WASM", filepath.Join(directory, "missing.wasm"))
	response := httptest.NewRecorder()
	new(studio).kernelWASM(response, httptest.NewRequest("GET", "/api/kernel.wasm?keys=1", nil))
	if response.Code != 503 || !strings.Contains(response.Body.String(), "CICADA_KEYS_WASM") {
		t.Fatalf("optional failure fell back to core: %d %s", response.Code, response.Body.String())
	}
}

func TestStudioKeysModuleSearchUsesSiblingAndBuildAncestors(t *testing.T) {
	directory := t.TempDir()
	working := filepath.Join(directory, "project", "nested")
	sibling := filepath.Join(directory, "bin")
	if err := os.MkdirAll(working, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0700); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(directory, "build")
	if err := os.MkdirAll(ancestor, 0700); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(ancestor, "cicada-keys.wasm")
	if err := os.WriteFile(buildFile, []byte("keys-build"), 0600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(sibling, "studio")
	if got := findAudioWASM("cicada-keys.wasm", executable, working); got != buildFile {
		t.Fatalf("ancestor search returned %q", got)
	}
	siblingFile := filepath.Join(sibling, "cicada-keys.wasm")
	if err := os.WriteFile(siblingFile, []byte("keys-sibling"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := findAudioWASM("cicada-keys.wasm", executable, working); got != siblingFile {
		t.Fatalf("sibling did not take priority: %q", got)
	}
	if got := findAudioWASM("missing.wasm", executable, working); got != "" {
		t.Fatalf("missing module returned %q", got)
	}
}
