package appdirs

import (
	"os"
	"path/filepath"
	"testing"
)

// Production behavior is the contract: with the variable unset, Cicada keeps
// using the OS directories under the same names it always has.
func TestDefaultsFollowTheOSDirectories(t *testing.T) {
	t.Setenv(StateEnv, "")

	config, configErr := os.UserConfigDir()
	state, err := StateDir()
	if (err != nil) != (configErr != nil) {
		t.Fatalf("StateDir error %v, os.UserConfigDir error %v", err, configErr)
	}
	if err == nil && state != filepath.Join(config, "Cicada Studio") {
		t.Fatalf("StateDir = %q, want %q", state, filepath.Join(config, "Cicada Studio"))
	}

	cache, cacheErr := os.UserCacheDir()
	got, err := CacheDir()
	if (err != nil) != (cacheErr != nil) {
		t.Fatalf("CacheDir error %v, os.UserCacheDir error %v", err, cacheErr)
	}
	if err == nil && got != filepath.Join(cache, "cicada") {
		t.Fatalf("CacheDir = %q, want %q", got, filepath.Join(cache, "cicada"))
	}
}

func TestStateEnvMovesStateAndCacheBeneathOneRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(StateEnv, root)
	// The XDG variables must not matter once the override is set, on any platform.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if state, err := StateDir(); err != nil || state != root {
		t.Fatalf("StateDir = %q, %v; want %q", state, err, root)
	}
	want := filepath.Join(root, "cache")
	if cache, err := CacheDir(); err != nil || cache != want {
		t.Fatalf("CacheDir = %q, %v; want %q", cache, err, want)
	}
}

func TestStateEnvIsMadeAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(StateEnv, "relative")
	want, err := filepath.Abs("relative")
	if err != nil {
		t.Fatal(err)
	}
	if state, err := StateDir(); err != nil || state != want {
		t.Fatalf("StateDir = %q, %v; want %q", state, err, want)
	}
	if cache, err := CacheDir(); err != nil || cache != filepath.Join(want, "cache") {
		t.Fatalf("CacheDir = %q, %v; want %q", cache, err, filepath.Join(want, "cache"))
	}
}
