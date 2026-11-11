package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"m31labs.dev/cicada/workstation/internal/enginebuild"
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
// (2026-11-10, make engine-size-report). This walks the whole js/wasm
// dependency graph, so no package the engine imports can bring the client
// back. The standard library may import net/http and crypto/tls among itself;
// GoSX's hub client imports net/http for one header type, which the linker
// keeps only as package initialization (remove it from the list when GoSX
// drops that import).
func TestBrowserEngineSendsRequestsThroughFetch(t *testing.T) {
	allowed := map[string]bool{"m31labs.dev/gosx/hub/client": true}
	for _, pkg := range engineGraph(t, enginebuild.Tags) {
		if pkg.standard || allowed[pkg.path] {
			continue
		}
		for _, dep := range pkg.imports {
			if dep == "net/http" || strings.HasPrefix(dep, "net/http/") || dep == "crypto/tls" {
				t.Errorf("%s imports %s; the browser engine sends requests with nativeRequest (engine/native_request.go)", pkg.path, dep)
			}
		}
	}
}

// The engine is built with enginebuild.Tags to leave GoSX's canvas-board
// adapter (and with it the image decoders) out. If a GoSX update makes the tag
// change anything else in the engine's graph, this fails, so the tag cannot
// silently remove code the engine uses.
func TestEngineBuildTagsLeaveOutOnlyTheCanvasBoard(t *testing.T) {
	files := func(tags string) map[string]string {
		out := map[string]string{}
		for _, pkg := range engineGraph(t, tags) {
			out[pkg.path] = strings.Join(pkg.files, ",")
		}
		return out
	}
	plain, tagged := files(""), files(enginebuild.Tags)
	removed := map[string]bool{"canvas_board_adapter.go": true, "canvas_board_server.go": true}
	for path, list := range tagged {
		before, ok := plain[path]
		if !ok {
			t.Errorf("the build tags add package %s to the engine", path)
			continue
		}
		if list == before {
			continue
		}
		var kept []string
		for _, name := range strings.Split(before, ",") {
			if !removed[name] {
				kept = append(kept, name)
			}
		}
		if path != "m31labs.dev/gosx/client/vm" || strings.Join(kept, ",") != list {
			t.Errorf("the build tags change the files of %s:\n without: %s\n with:    %s", path, before, list)
		}
	}
	if strings.Contains(tagged["m31labs.dev/gosx/client/vm"], "canvas_board_adapter.go") {
		t.Error("the build tags no longer leave out client/vm's canvas-board adapter")
	}
	for _, gone := range []string{"m31labs.dev/gosx/render/bundle", "image/jpeg", "image/png", "image/gif"} {
		if _, ok := tagged[gone]; ok {
			t.Errorf("the engine still links %s", gone)
		}
	}
}

type graphPackage struct {
	path     string
	standard bool
	files    []string
	imports  []string
}

// engineGraph lists every package of the js/wasm engine build.
func engineGraph(t *testing.T, tags string) []graphPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-tags="+tags, "-deps", "-f", "{{.ImportPath}}\t{{.Standard}}\t{{join .GoFiles \",\"}}\t{{join .Imports \",\"}}", "./engine")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser dependency graph: %v", err)
	}
	var graph []graphPackage
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			t.Fatalf("unexpected go list line %q", line)
		}
		split := func(s string) []string {
			if s == "" {
				return nil
			}
			return strings.Split(s, ",")
		}
		graph = append(graph, graphPackage{path: f[0], standard: f[1] == "true", files: split(f[2]), imports: split(f[3])})
	}
	return graph
}
