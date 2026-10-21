package isolatetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/appdirs"
)

func TestStateConfinesAppdirsAndRestoresTheEnvironment(t *testing.T) {
	before, beforeSet := os.LookupEnv(appdirs.StateEnv)
	var dir string
	t.Run("isolated", func(t *testing.T) {
		dir = State(t)
		if state, err := appdirs.StateDir(); err != nil || state != dir {
			t.Fatalf("StateDir = %q, %v; want %q", state, err, dir)
		}
		if cache, err := appdirs.CacheDir(); err != nil || cache != filepath.Join(dir, "cache") {
			t.Fatalf("CacheDir = %q, %v; want %q", cache, err, filepath.Join(dir, "cache"))
		}
		if got := os.Getenv("XDG_CONFIG_HOME"); got != dir {
			t.Fatalf("XDG_CONFIG_HOME = %q, want %q", got, dir)
		}
	})
	if got, set := os.LookupEnv(appdirs.StateEnv); got != before || set != beforeSet {
		t.Fatalf("%s leaked out of the test: %q (set=%v), was %q (set=%v)", appdirs.StateEnv, got, set, before, beforeSet)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("state directory outlived the test: %v", err)
	}
}

func TestEnvMatchesWhatUseSets(t *testing.T) {
	dir := t.TempDir()
	Use(t, dir)
	for _, entry := range Env(dir) {
		key, value, _ := strings.Cut(entry, "=")
		if got := os.Getenv(key); got != value {
			t.Errorf("Env entry %s=%q disagrees with Use, which set %q", key, value, got)
		}
	}
	if len(Env(dir)) != 2 {
		t.Fatalf("Env = %v", Env(dir))
	}
}

func TestUseAcceptsACallerOwnedDirectory(t *testing.T) {
	dir := t.TempDir()
	Use(t, dir)
	if state, err := appdirs.StateDir(); err != nil || state != dir {
		t.Fatalf("StateDir = %q, %v; want %q", state, err, dir)
	}
}
