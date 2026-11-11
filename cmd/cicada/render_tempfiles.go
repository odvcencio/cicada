package main

import (
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"m31labs.dev/cicada/render"
)

// renderNoReuseEnv names the environment variable that turns off every reuse
// of rendered audio: CICADA_RENDER_NO_REUSE=1 makes a loudness search render
// every pass in full (no stored mix, no repeated passes) and peak
// normalization render twice in full. The output is the same either way; the
// variable is there to work around, and to bisect, a suspected difference.
const renderNoReuseEnv = "CICADA_RENDER_NO_REUSE"

// renderReuseAllowed reports whether renders may reuse audio: the stored mix
// (render.MasterCache) and passes that would repeat an earlier pass.
func renderReuseAllowed() bool { return os.Getenv(renderNoReuseEnv) != "1" }

// renderTempPatterns are the names of the temporary files that renders write
// next to their output: the plain render's WAV before it replaces the output,
// the loudness search's candidate and best-so-far WAVs, and the stored mix,
// which has a name only where an open file cannot lose it (Windows).
var renderTempPatterns = []string{".cicada-render-*", ".cicada-loudness-candidate-*", ".cicada-loudness-best-*", render.MasterCacheFilePattern}

// staleRenderFileAge is how old a render's temporary file must be before a
// later render in the same folder removes it as left behind.
const staleRenderFileAge = 24 * time.Hour

// removeStaleRenderFiles removes the render temporary files in dir that are
// more than a day old. A render removes its own files when it ends, and when
// it is interrupted (see armInterruptCleanup); these are what a killed or
// crashed render left behind. No render runs that long, so a file this old
// belongs to none. Errors are ignored: this only gives space back.
func removeStaleRenderFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !matchesRenderTemp(entry.Name()) {
			continue
		}
		if info, err := entry.Info(); err == nil && now.Sub(info.ModTime()) > staleRenderFileAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func matchesRenderTemp(name string) bool {
	for _, pattern := range renderTempPatterns {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// interruptFiles are the temporary files to remove if the process is
// interrupted.
var interruptFiles = struct {
	sync.Mutex
	paths map[string]int
}{paths: map[string]int{}}

// removeOnInterrupt registers path for removal if the process is interrupted
// and returns the function that takes it off the list again.
func removeOnInterrupt(path string) (done func()) {
	interruptFiles.Lock()
	interruptFiles.paths[path]++
	interruptFiles.Unlock()
	return func() {
		interruptFiles.Lock()
		if interruptFiles.paths[path]--; interruptFiles.paths[path] <= 0 {
			delete(interruptFiles.paths, path)
		}
		interruptFiles.Unlock()
	}
}

// armInterruptCleanup makes an interrupt (SIGINT, or SIGTERM where it exists)
// remove the registered temporary files, stop a CPU profile and exit with
// status 130, as an interrupted shell command does. A kill that cannot be
// caught skips it; removeStaleRenderFiles clears what that leaves.
func armInterruptCleanup() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		removeInterruptFiles()
		exitProcess(130)
	}()
}

func removeInterruptFiles() {
	interruptFiles.Lock()
	defer interruptFiles.Unlock()
	for path := range interruptFiles.paths {
		_ = os.Remove(path)
	}
}
