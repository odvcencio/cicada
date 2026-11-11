// Package enginebuild holds the command that builds Studio's UI engine
// WebAssembly, so `go generate` and the size report build the same file.
package enginebuild

import (
	"os"
	"os/exec"
)

// Package is the engine's main package, relative to the workstation module.
const Package = "./engine"

// Tags are the build tags of every engine build. GoSX's gosx_runtime_core
// tag leaves out client/vm's canvas-board adapter, the only client/vm file
// that imports render/bundle and so the JPEG, PNG and GIF decoders; the engine
// uses client/vm only for ReconcileTrees. In the engine's dependency graph
// the tag changes nothing else (TestEngineBuildTagsLeaveOutOnlyTheCanvasBoard).
const Tags = "gosx_runtime_core"

// ReleaseLDFlags strips the symbol and name sections from the shipped file.
// The size report links without -s to keep function names for attribution.
const ReleaseLDFlags = "-s -w"

// MaxBytes is the working limit on the raw bytes of the shipped engine file;
// `go generate` fails above it.
//
// Measured on 2026-11-11: 11,130,262 bytes, after the engine stopped calling
// net/http's client and was built with Tags (make engine-size-report). The
// limit is that size plus 524,288 bytes (512 KiB), rounded up to a multiple of
// 65,536 (the engine modules design, step S2). Raise it only through a
// recorded decision, and never above Ceiling.
const MaxBytes = 11_665_408

// Ceiling is the most the engine may ever be: 16,777,216 bytes (16 MiB),
// decision 6 of decisions/2026-11-10-stewardship-and-direction.
const Ceiling = 16 << 20

// Env returns the environment of every engine build: the host environment
// with the browser target and no workspace.
func Env() []string {
	return append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
}

// Command returns `go build` for the engine with the given linker flags.
// Extra arguments (for example -overlay) come before the package.
func Command(output, ldflags string, extra ...string) *exec.Cmd {
	return CommandFor(Package, output, ldflags, extra...)
}

// CommandFor is Command for another main package, built the same way. The
// size report uses it for its runtime-floor programs.
func CommandFor(pkg, output, ldflags string, extra ...string) *exec.Cmd {
	args := []string{"build", "-trimpath", "-buildvcs=false", "-tags=" + Tags, "-ldflags=" + ldflags, "-o", output}
	args = append(args, extra...)
	args = append(args, pkg)
	command := exec.Command("go", args...)
	command.Env = Env()
	return command
}
