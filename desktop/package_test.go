//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the real packaging script with fake compilers and signing tools.
// This keeps failure-path coverage independent of macOS and a local GoSX tree.
func packageFixture(t *testing.T) (root, output string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "repo with spaces")
	for _, dir := range []string{"desktop", "workstation", "bin", "tmp", "output/Cicada Studio.app"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, contents string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("package.sh")
	if err != nil {
		t.Fatal(err)
	}
	write("desktop/package.sh", string(script), 0o755)
	write("output/Cicada Studio.app/previous", "keep until verified", 0o644)
	write("bin/tool", `#!/bin/bash
set -euo pipefail
name="${0##*/}"
case "$name" in
go)
  [[ "$GOOS" == darwin && "$CGO_ENABLED" == 0 ]] || exit 1
  if [[ "$1" == run && "$2" == */check-platform.go ]]; then
    [[ "$GOWORK" == "$EXPECTED_WORKSPACE" ]] || exit 1
    [[ "${FAIL_AT:-}" != backend ]] || exit 1
  elif [[ "$1" == run && "$2" == -mod=mod ]]; then
    [[ "$GOWORK" == off && "$3" == m31labs.dev/gosx/cmd/gosx ]] || exit 1
    [[ "$4 $5 $6" == 'build --prod .' ]] || exit 1
    [[ "${FAIL_AT:-}" != runtime ]] || exit 1
    mkdir -p dist/server dist/assets/runtime dist/public
    printf 'generated server\n' > dist/server/app
    chmod +x dist/server/app
    printf '{}\n' > dist/build.json
    printf 'runtime\n' > dist/assets/runtime/bootstrap.js
    printf 'engine\n' > dist/public/engine.wasm
  elif [[ "$1" == build ]]; then
    case "$PWD" in
      */desktop) [[ "$GOWORK" == "$EXPECTED_WORKSPACE" && "${FAIL_AT:-}" != host ]] || exit 1 ;;
      */workstation) echo 'unexpected second workstation compilation' >&2; exit 1 ;;
      *) [[ "$GOWORK" == off ]] || exit 1 ;;
    esac
    while [[ "$1" != -o ]]; do shift; done
    printf 'compiled binary\n' > "$2"
    chmod +x "$2"
  else
    echo "unexpected go invocation: $*" >&2
    exit 1
  fi
  ;;
codesign)
  [[ -f "$PREVIOUS_APP/previous" ]] || exit 1
  if [[ "$1" == --verify ]]; then
    [[ "${FAIL_AT:-}" != verification ]] || exit 1
  else
    [[ "${FAIL_AT:-}" != signing ]] || exit 1
  fi
  ;;
esac
`, 0o755)
	for _, tool := range []string{"go", "tinygo", "wasm-opt", "codesign", "plutil"} {
		if err := os.Symlink("tool", filepath.Join(root, "bin", tool)); err != nil {
			t.Fatal(err)
		}
	}
	output = filepath.Join(root, "output")
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", filepath.Join(root, "tmp")+string(os.PathSeparator))
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	t.Setenv("EXPECTED_WORKSPACE", filepath.Join(root, "go.work"))
	t.Setenv("PREVIOUS_APP", filepath.Join(output, "Cicada Studio.app"))
	t.Setenv("FAIL_AT", "")
	t.Setenv("WASMOPT", filepath.Join(root, "bin", "wasm-opt"))
	return root, output
}

func runPackage(t *testing.T, root, output, version string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, "desktop", "package.sh"),
		"--target", "darwin", "--version", version, "--output", output)
	cmd.Dir = t.TempDir() // Packaging must work outside the repository.
	return cmd.CombinedOutput()
}

func assertPackageCleanedUp(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary packaging files left behind: %v, %v", entries, err)
	}
}

func TestPackageDarwinUsesWorkspaceOnlyForHost(t *testing.T) {
	root, output := packageFixture(t)
	if log, err := runPackage(t, root, output, "1.2.3-rc.1+build.8"); err != nil {
		t.Fatalf("package: %v\n%s", err, log)
	}
	app := filepath.Join(output, "Cicada Studio.app")
	if _, err := os.Stat(filepath.Join(app, "previous")); !os.IsNotExist(err) {
		t.Fatalf("previous app was not replaced: %v", err)
	}
	for _, file := range []string{"cicada-studio", "cicada", "cicada-workstation"} {
		info, err := os.Stat(filepath.Join(app, "Contents", "MacOS", file))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("missing executable %s: %v", file, err)
		}
	}
	for file, want := range map[string]string{
		"Info.plist":                                    "<string>1.2.3</string>",
		"MacOS/cicada-workstation":                      "generated server",
		"MacOS/workstation/build.json":                  "{}",
		"MacOS/workstation/assets/runtime/bootstrap.js": "runtime",
		"MacOS/workstation/public/engine.wasm":          "engine",
	} {
		contents, err := os.ReadFile(filepath.Join(app, "Contents", file))
		if err != nil || !strings.Contains(string(contents), want) {
			t.Fatalf("bundle file %s = %q, %v; want %q", file, contents, err, want)
		}
	}
	assertPackageCleanedUp(t, root)
}

func TestPackageDarwinFailurePreservesPreviousApp(t *testing.T) {
	for _, stage := range []string{"backend", "host", "runtime", "signing", "verification", "prerequisite"} {
		t.Run(stage, func(t *testing.T) {
			root, output := packageFixture(t)
			t.Setenv("FAIL_AT", stage)
			if stage == "prerequisite" {
				t.Setenv("WASMOPT", filepath.Join(root, "missing-wasm-opt"))
			}
			if log, err := runPackage(t, root, output, "1.2.3"); err == nil {
				t.Fatalf("expected %s failure, got success:\n%s", stage, log)
			}
			contents, err := os.ReadFile(filepath.Join(output, "Cicada Studio.app", "previous"))
			if err != nil || string(contents) != "keep until verified" {
				t.Fatalf("previous app changed after %s failure: %q, %v", stage, contents, err)
			}
			assertPackageCleanedUp(t, root)
		})
	}
}

func TestPackageDarwinRejectsInvalidBundleVersion(t *testing.T) {
	for _, version := range []string{"dev", "1.2", "1.2.3.4", "1_2_3", "1.2.3-", "1.2.3<"} {
		t.Run(version, func(t *testing.T) {
			root, output := packageFixture(t)
			log, err := runPackage(t, root, output, version)
			if err == nil || !strings.Contains(string(log), "invalid --version") {
				t.Fatalf("version %q accepted or failed for another reason: %v\n%s", version, err, log)
			}
		})
	}
}
