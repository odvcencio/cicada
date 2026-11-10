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

// The engine sends requests through the browser's fetch (engine/fetch_js.go).
// Calling net/http's client linked its js/wasm transport, which misreported
// finished responses, and its TLS, HTTP/2 and certificate code: moving nine
// call sites to fetch took the engine from 16,733,505 to 11,493,199 bytes
// (2026-11-10, make engine-size-report). GoSX's hub client still imports
// net/http for one header type, so this checks the engine's own imports
// rather than the whole graph.
func TestBrowserEngineSendsRequestsThroughFetch(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./engine")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser engine imports: %v\n%s", err, output)
	}
	for _, dep := range strings.Fields(string(output)) {
		if dep == "net/http" || strings.HasPrefix(dep, "net/http/") || dep == "crypto/tls" {
			t.Errorf("browser engine imports %s; send requests with nativeRequest (engine/native_request.go)", dep)
		}
	}
}
