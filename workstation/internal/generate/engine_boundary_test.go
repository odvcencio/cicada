package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The UI exchanges projections with the native service. Compiler/parser and
// DSP dependencies here doubled the engine payload and delayed Studio startup.
func TestBrowserEngineKeepsHostDependenciesOut(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./engine")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser dependency graph: %v\n%s", err, output)
	}
	for _, dep := range strings.Fields(string(output)) {
		if dep == "m31labs.dev/cicada/project" || dep == "m31labs.dev/cicada/notation" || dep == "m31labs.dev/cicada/kernel/engine" {
			t.Errorf("browser engine links host dependency %s", dep)
		}
	}
}
