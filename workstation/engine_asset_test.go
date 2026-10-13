package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
)

func writeEngine(t *testing.T, public, name string, body []byte) {
	t.Helper()
	dir := filepath.Join(public, "engines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func engineName(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("cicada-meters.%x.wasm", sum[:8])
}

func TestResolveMeterEngineFindsGeneratedFile(t *testing.T) {
	public := t.TempDir()
	body := []byte("\x00asm-meters-current")
	writeEngine(t, public, engineName(body), body)
	got, err := resolveMeterEngine(public)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^/engines/cicada-meters\.[0-9a-f]{16}\.wasm$`).MatchString(got) {
		t.Fatalf("path %q has the wrong shape", got)
	}
	if want := "/engines/" + engineName(body); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestResolveMeterEngineMissingReturnsError(t *testing.T) {
	for _, public := range []string{t.TempDir(), filepath.Join(t.TempDir(), "absent"), ""} {
		if got, err := resolveMeterEngine(public); err == nil || got != "" {
			t.Fatalf("public %q: got %q, %v; want an error", public, got, err)
		}
	}
}

func TestResolveMeterEngineStaleFileLoses(t *testing.T) {
	body := []byte("\x00asm-meters-current")
	stale := []byte("\x00asm-meters-old")
	// Try both name orders so directory order cannot decide the result.
	for _, staleName := range []string{"cicada-meters.0000000000000000.wasm", "cicada-meters.ffffffffffffffff.wasm"} {
		public := t.TempDir()
		writeEngine(t, public, staleName, stale)
		writeEngine(t, public, engineName(body), body)
		for i := 0; i < 3; i++ {
			got, err := resolveMeterEngine(public)
			if err != nil || got != "/engines/"+engineName(body) {
				t.Fatalf("stale %s: got %q, %v", staleName, got, err)
			}
		}
	}
}

func TestResolveMeterEngineOnlyStaleFilesFail(t *testing.T) {
	public := t.TempDir()
	writeEngine(t, public, "cicada-meters.0123456789abcdef.wasm", []byte("not that hash"))
	if got, err := resolveMeterEngine(public); err == nil || got != "" {
		t.Fatalf("got %q, %v; want an error", got, err)
	}
}

// TestMain gives page tests a generated-looking engine so they keep covering
// the mounted engine markup without running go generate.
func TestMain(m *testing.M) {
	public, err := os.MkdirTemp("", "cicada-engine-test")
	if err != nil {
		panic(err)
	}
	body := []byte("\x00asm-test-meters")
	dir := filepath.Join(public, "engines")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(dir, engineName(body)), body, 0o644); err != nil {
		panic(err)
	}
	meterEnginePublicDir = func() string { return public }
	code := m.Run()
	os.RemoveAll(public)
	os.Exit(code)
}

func useMeterEngineDir(t *testing.T, public string) {
	t.Helper()
	old, oldPath, oldLogged := meterEnginePublicDir, meterEngineCached, meterEngineLogged
	meterEnginePublicDir = func() string { return public }
	meterEngineCached, meterEngineLogged = "", false
	t.Cleanup(func() {
		meterEnginePublicDir, meterEngineCached, meterEngineLogged = old, oldPath, oldLogged
	})
}

func TestMeterEngineFallsBackWithoutAsset(t *testing.T) {
	useMeterEngineDir(t, t.TempDir())
	if got := meterEnginePath(); got != "" {
		t.Fatalf("unexpected path %q", got)
	}
	// The fallback text renders and the engine is not mounted.
	node := meterEngine(nil, engine.Config{Name: "X"}, gosx.Text("Live output meters require browser WASM and canvas support."))
	if got := gosx.RenderHTML(node); !strings.Contains(got, "Live output meters require") {
		t.Fatalf("fallback missing: %q", got)
	}
}

func TestMeterEnginePathUsesResolvedFile(t *testing.T) {
	public := t.TempDir()
	body := []byte("\x00asm-other")
	writeEngine(t, public, engineName(body), body)
	useMeterEngineDir(t, public)
	if got := meterEnginePath(); got != "/engines/"+engineName(body) {
		t.Fatalf("got %q", got)
	}
}

// Two builds that both match their bytes (for example a release directory
// that kept an older build) are ambiguous: neither wins.
func TestResolveMeterEngineRefusesTwoValidFiles(t *testing.T) {
	public := t.TempDir()
	for _, body := range [][]byte{[]byte("\x00asm-meters-a"), []byte("\x00asm-meters-b")} {
		writeEngine(t, public, engineName(body), body)
	}
	got, err := resolveMeterEngine(public)
	if err == nil || got != "" || !strings.Contains(err.Error(), "2 cicada-meters") {
		t.Fatalf("got %q, %v; want an ambiguity error", got, err)
	}
}

// A missing asset is not cached: once go generate writes it, the next request
// finds it without a restart.
func TestMeterEnginePathRecoversAfterGenerate(t *testing.T) {
	public := t.TempDir()
	useMeterEngineDir(t, public)
	if got := meterEnginePath(); got != "" {
		t.Fatalf("before generate: %q", got)
	}
	body := []byte("\x00asm-generated-later")
	writeEngine(t, public, engineName(body), body)
	if got := meterEnginePath(); got != "/engines/"+engineName(body) {
		t.Fatalf("after generate: %q", got)
	}
}
