package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserEngineDoesNotImportCompiler(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./engine")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect browser dependencies: %v\n%s", err, output)
	}
	for _, path := range strings.Fields(string(output)) {
		switch path {
		case "m31labs.dev/cicada/host/padsampler", "m31labs.dev/cicada/project", "m31labs.dev/cicada/edit", "m31labs.dev/cicada/notation":
			t.Errorf("browser engine imports host/compiler package %s; use a portable protocol", path)
		}
	}
}

func TestGenerateMultipleComponentsAndIsland(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app/ui"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"first.gsx": `package ui
type FirstProps struct { Text string }
component First(props: FirstProps) {
	return <p>{props.Text}</p>
}`,
		"second.gsx": `package ui
import "m31labs.dev/gosx/signal"
//gosx:island
func Second() Node {
 count := signal.New(0)
 increment := func() { count.Set(count.Get() + 1) }
 return <button onClick={increment}>{count.Get()}</button>
}`,
		"first.css": "p { color: red; }", "second.css": "button { color: green; }",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, "app/ui", name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := generateUI(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first_gsx.go", "second_gsx.go", "islands.go", "style_asset.go"} {
		if _, err := os.Stat(filepath.Join(root, "ui", name)); err != nil {
			t.Fatal(err)
		}
	}
	programs, err := os.ReadFile(filepath.Join(root, "ui/islands.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(programs), `"Second"`) {
		t.Fatal("second file's island program is absent")
	}
	css, _ := os.ReadFile(filepath.Join(root, "ui/style_asset.go"))
	if !strings.Contains(string(css), "color: green") || !strings.Contains(string(css), "color: red") {
		t.Fatal("stylesheets were not combined")
	}
}
