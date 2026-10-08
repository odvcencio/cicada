package edit_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestEditCoreStaysPure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	cmd := exec.Command("go", "list", "-deps", "m31labs.dev/cicada/edit", "m31labs.dev/cicada/edit/editlog")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case dep == "net/http", dep == "os/exec", dep == "m31labs.dev/cicada/project", dep == "m31labs.dev/cicada/render",
			dep == "m31labs.dev/cicada/migration", strings.HasPrefix(dep, "m31labs.dev/cicada/host/"):
			t.Errorf("edit core depends on %s", dep)
		}
	}
}

func TestEditCoreBuildsForWASM(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	cmd := exec.Command("go", "build", "m31labs.dev/cicada/edit", "m31labs.dev/cicada/edit/editlog")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasm build: %v\n%s", err, out)
	}
}
