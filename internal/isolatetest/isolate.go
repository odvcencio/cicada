// Package isolatetest keeps tests away from the developer's real per-user
// Cicada directories.
//
// Pointing XDG_CONFIG_HOME at a temporary directory is not enough: Go ignores it
// on macOS, so Studio recovery files and the editor cache would land in
// ~/Library. appdirs.StateEnv is honored on every platform; see internal/appdirs.
//
// Isolate a test with
//
//	isolatetest.State(t)
//
// and give a package a safety net for tests that forget, with
//
//	func TestMain(m *testing.M) { os.Exit(isolatetest.Run(m)) }
package isolatetest

import (
	"fmt"
	"os"
	"testing"

	"m31labs.dev/cicada/internal/appdirs"
)

// State points Cicada's per-user state and cache at a fresh temporary directory
// until t finishes, and returns that directory. Like t.Setenv, it cannot be
// used from a parallel test.
func State(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Use(t, dir)
	return dir
}

// Use is State for a directory the test already owns.
func Use(t testing.TB, dir string) {
	t.Helper()
	for _, setting := range settings(dir) {
		t.Setenv(setting[0], setting[1])
	}
}

// Env returns the environment entries Use sets, for a child process whose
// environment the test assembles itself:
//
//	cmd.Env = append(os.Environ(), isolatetest.Env(dir)...)
//
// A child that inherits the test's environment is already isolated.
func Env(dir string) []string {
	var env []string
	for _, setting := range settings(dir) {
		env = append(env, setting[0]+"="+setting[1])
	}
	return env
}

func settings(dir string) [][2]string {
	return [][2]string{
		{appdirs.StateEnv, dir},
		// Linux still resolves os.UserConfigDir from XDG_CONFIG_HOME. Keeping it
		// inside dir there confines the remaining readers of the config directory,
		// such as the default user library location, as these tests always were.
		{"XDG_CONFIG_HOME", dir},
	}
}

// Run is for TestMain. It gives every test in the package a private state
// directory, removed when the package's tests finish, so a test that does not
// call State still cannot reach the developer's real directories.
//
// An existing appdirs.StateEnv is kept rather than replaced. A test that
// re-executes its own binary relies on the child sharing the parent's state, and
// State's per-test directory must survive into that child.
func Run(m *testing.M) int {
	if os.Getenv(appdirs.StateEnv) == "" {
		dir, err := os.MkdirTemp("", "cicada-state-")
		if err != nil {
			fmt.Fprintln(os.Stderr, "isolatetest:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		if err := os.Setenv(appdirs.StateEnv, dir); err != nil {
			fmt.Fprintln(os.Stderr, "isolatetest:", err)
			return 1
		}
	}
	return m.Run()
}
