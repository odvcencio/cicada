package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

var meterEngineName = regexp.MustCompile(`^cicada-meters\.([0-9a-f]{16})\.wasm$`)

// resolveMeterEngine returns the public URL of the content-addressed meters
// WASM under publicDir/engines. A file counts only when the hash in its name
// equals the SHA-256 prefix of its bytes, so stale builds never win and
// directory order never decides the result.
func resolveMeterEngine(publicDir string) (string, error) {
	if publicDir == "" {
		return "", errors.New("no public directory")
	}
	dir := filepath.Join(publicDir, "engines")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("meters engine not found: %w", err)
	}
	var matches []string
	stale := 0
	for _, entry := range entries {
		m := meterEngineName.FindStringSubmatch(entry.Name())
		if m == nil || !entry.Type().IsRegular() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(body)
		if fmt.Sprintf("%x", sum[:8]) == m[1] {
			matches = append(matches, entry.Name())
		} else {
			stale++
		}
	}
	switch len(matches) {
	case 1:
		return "/engines/" + matches[0], nil
	case 0:
		return "", fmt.Errorf("no cicada-meters.<hash>.wasm in %s matches its bytes (%d stale); run go generate", dir, stale)
	default:
		return "", fmt.Errorf("%d cicada-meters.*.wasm files in %s match their bytes; remove the extras", len(matches), dir)
	}
}

var (
	meterEngineMu     sync.Mutex
	meterEngineCached string
	meterEngineLogged bool
)

// meterEnginePublicDir locates the public directory that holds the generated
// engine. Tests replace it.
var meterEnginePublicDir = func() string {
	if root := runtimeRoot(); root != "" {
		return filepath.Join(root, "public")
	}
	return ""
}

// meterEnginePath resolves the meters engine on first use and caches a
// success. It returns "" when the asset is missing and logs that once.
func meterEnginePath() string {
	meterEngineMu.Lock()
	defer meterEngineMu.Unlock()
	if meterEngineCached != "" {
		return meterEngineCached
	}
	public := meterEnginePublicDir()
	if public == "" {
		return ""
	}
	path, err := resolveMeterEngine(public)
	if err != nil {
		if !meterEngineLogged {
			meterEngineLogged = true
			log.Printf("workstation: live engine disabled: %v", err)
		}
		return ""
	}
	meterEngineCached = path
	return path
}

// meterEngine mounts a Go/WASM surface from the meters engine. When the asset
// is missing it renders only the fallback so the rest of the page still works.
func meterEngine(ctx *server.Context, cfg engine.Config, fallback gosx.Node) gosx.Node {
	path := meterEnginePath()
	if path == "" {
		return fallback
	}
	cfg.WASMPath = path
	return ctx.Engine(cfg, fallback)
}
