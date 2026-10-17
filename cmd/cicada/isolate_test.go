package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/appdirs"
	"m31labs.dev/cicada/internal/isolatetest"
)

// Studio keeps displaced scores and the editor cache in per-user directories.
// Go ignores XDG_CONFIG_HOME on macOS, so only appdirs.StateEnv keeps those
// files out of the developer's real Library. isolateState gives a test its own
// directory; TestMain covers any test that does not ask for one.
func TestMain(m *testing.M) { os.Exit(isolatetest.Run(m)) }

// isolateState confines Studio state and the editor cache to a fresh directory
// until the test ends, and returns it.
func isolateState(t *testing.T) string {
	t.Helper()
	return isolatetest.State(t)
}

// isolatedState returns the state directory now in force, for tests that must
// show an operation left nothing behind there.
func isolatedState(t *testing.T) string {
	t.Helper()
	dir, err := appdirs.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A recovery directory resolved through os.UserConfigDir escapes isolation that
// sets only XDG_CONFIG_HOME on macOS, where it lands in ~/Library. Passing here
// on macOS means every save the other tests make stays inside their directory.
func TestStudioRecoveryDirStaysInsideIsolatedState(t *testing.T) {
	state := isolateState(t)
	score := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(score, []byte(studioScore), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := studioRecoveryDir(score)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, state+string(filepath.Separator)) {
		t.Fatalf("recovery directory %s is outside the isolated state directory %s", dir, state)
	}
}
