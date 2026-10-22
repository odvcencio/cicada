//go:build !windows

package buildaudit

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type modPin struct{ Path, Version string }
type moduleFile struct {
	Go, Toolchain string
	Require       []modPin
	Replace       []struct{ Old, New modPin }
	Tool          []struct{ Path string }
}

func readModule(t *testing.T, path string) moduleFile {
	t.Helper()
	cmd := exec.Command("go", "mod", "edit", "-json", path)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read module: %v: %s", err, output)
	}
	var m moduleFile
	if err := json.Unmarshal(output, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func selectedSDK(m moduleFile) (modPin, *modPin) {
	var sdk modPin
	var replacement *modPin
	for _, r := range m.Require {
		if r.Path == "m31labs.dev/gosx" {
			sdk = r
		}
	}
	for _, r := range m.Replace {
		if r.Old.Path == sdk.Path && (r.Old.Version == "" || r.Old.Version == sdk.Version) {
			v := r.New
			replacement = &v
		}
	}
	return sdk, replacement
}
func TestCanonicalToolUsesTheWorkstationSDKWithoutAppCompressors(t *testing.T) {
	root := repoRoot(t)
	app := readModule(t, filepath.Join(root, "workstation", "go.mod"))
	tool := readModule(t, filepath.Join(root, "tools", "gosx-size", "go.mod"))
	appSDK, appReplacement := selectedSDK(app)
	toolSDK, toolReplacement := selectedSDK(tool)
	if appSDK.Path == "" || appSDK != toolSDK || !reflect.DeepEqual(appReplacement, toolReplacement) {
		t.Fatal("update the canonical tool when changing the workstation GoSX pin", appSDK, appReplacement, toolSDK, toolReplacement)
	}
	if tool.Go != "1.26.0" || tool.Toolchain != "" && tool.Toolchain != "go1.26.0" {
		t.Fatal("canonical Go compiler pin changed", tool.Go, tool.Toolchain)
	}
	compressor := ""
	for _, dep := range tool.Require {
		if dep.Path == "github.com/andybalholm/brotli" {
			compressor = dep.Version
		}
	}
	if compressor != "v1.2.1" {
		t.Fatal("canonical Brotli pin changed", compressor)
	}
	for _, r := range tool.Replace {
		if r.New.Version == "" || r.Old.Path == "github.com/andybalholm/brotli" {
			t.Fatal("canonical tool must use versioned SDK code and an unreplaced compressor", r)
		}
	}
	found := false
	for _, entry := range tool.Tool {
		found = found || entry.Path == "m31labs.dev/gosx/cmd/gosx"
	}
	if !found {
		t.Fatal("GoSX tool declaration missing")
	}
}

func TestCanonicalWrapperPreservesPathsIdentityAndFailureCleanup(t *testing.T) {
	for _, failAt := range []string{"", "build", "audit"} {
		t.Run(failAt, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repo with spaces")
			caller := filepath.Join(t.TempDir(), "caller with spaces")
			temp := filepath.Join(t.TempDir(), "temporary files")
			for _, dir := range []string{filepath.Join(root, "scripts"), filepath.Join(root, "tools", "gosx-size"), filepath.Join(root, "bin"), caller, temp} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "check-gosx-size.sh"))
			if err != nil {
				t.Fatal(err)
			}
			scriptPath := filepath.Join(root, "scripts", "check-gosx-size.sh")
			if err := os.WriteFile(scriptPath, script, 0755); err != nil {
				t.Fatal(err)
			}
			modulePath := filepath.Join(root, "tools", "gosx-size", "go.mod")
			before := []byte("module fixture\n")
			if err := os.WriteFile(modulePath, before, 0644); err != nil {
				t.Fatal(err)
			}
			fake := `#!/bin/bash
set -euo pipefail
[[ "$GOWORK" == off && "$GOTOOLCHAIN" == go1.26.0 && -z "$GOOS" && -z "$GOARCH" && "$CGO_ENABLED" == 0 ]] || exit 91
[[ "$1" == -C && "$2" == "$EXPECTED_MODULE" && "$3" == build ]] || exit 92
[[ "$*" == *-mod=readonly* && "$*" == *"-modfile $EXPECTED_MODULE/go.mod"* ]] || exit 93
[[ "$FAIL_AT" != build ]] || exit 17
while [[ "$1" != -o ]]; do shift; done
cat > "$2" <<'AUDIT'
#!/bin/bash
set -euo pipefail
printf '%s\n' "$PWD" "$@" > "$COMMAND_LOG"
[[ "$FAIL_AT" != audit ]] || exit 19
printf '{"ok":true}\n'
AUDIT
chmod +x "$2"
`
			if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte(fake), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMPDIR", temp)
			t.Setenv("FAIL_AT", failAt)
			t.Setenv("EXPECTED_MODULE", filepath.Dir(modulePath))
			t.Setenv("COMMAND_LOG", filepath.Join(root, "command.log"))
			t.Setenv("GOWORK", "hostile go.work")
			t.Setenv("GOTOOLCHAIN", "go1.26.5")
			t.Setenv("GOOS", "windows")
			t.Setenv("GOARCH", "amd64")
			t.Setenv("CGO_ENABLED", "1")
			t.Setenv("GOFLAGS", "-modfile=app.go.mod -p=2")
			cmd := exec.Command("bash", scriptPath, "--json", "relative app/build.json")
			cmd.Dir = caller
			output, err := cmd.CombinedOutput()
			if (err != nil) != (failAt != "") {
				t.Fatalf("wrong audit result: %v %s", err, output)
			}
			if failAt == "" || failAt == "audit" {
				data, err := os.ReadFile(filepath.Join(root, "command.log"))
				if err != nil {
					t.Fatal(err)
				}
				want := caller + "\nsize\n--json\nrelative app/build.json\n"
				if string(data) != want {
					t.Fatal("caller directory or arguments changed", string(data))
				}
			}
			after, _ := os.ReadFile(modulePath)
			if !bytes.Equal(after, before) {
				t.Fatal("tool module was modified")
			}
			entries, err := os.ReadDir(temp)
			if err != nil || len(entries) != 0 {
				t.Fatal("temporary tool binary was not cleaned up", entries, err)
			}
		})
	}
}

func TestCanonicalWrapperRequiresATargetBeforeBuilding(t *testing.T) {
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "check-gosx-size.sh"))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "usage:") {
		t.Fatal("missing target did not explain usage", err, string(output))
	}
}

// Opt-in on a real production build. Unit tests need no SDK download; CI sets
// this after building the runtime so the unchanged canonical verifier is also
// exercised through the wrapper, including its corrupt-artifact failure path.
func TestCanonicalAuditAcceptsProductionAndRefusesCorruptRepresentations(t *testing.T) {
	manifestPath := os.Getenv("CICADA_SIZE_AUDIT_MANIFEST")
	if manifestPath == "" {
		t.Skip("set CICADA_SIZE_AUDIT_MANIFEST to a production build.json")
	}
	manifestPath, err := filepath.Abs(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Dir(manifestPath)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Runtime struct{ WASM struct{ File string } }
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	name := manifest.Runtime.WASM.File
	if name == "" || filepath.Base(name) != name {
		t.Fatal("production WASM filename is missing or escapes its asset root")
	}
	script := filepath.Join(repoRoot(t), "scripts", "check-gosx-size.sh")
	run := func(path string) ([]byte, error) {
		cmd := exec.Command("bash", script, "--json", path)
		cmd.Dir = t.TempDir()
		return cmd.CombinedOutput()
	}
	output, err := run(manifestPath)
	if err != nil {
		t.Fatalf("production audit: %v: %s", err, output)
	}
	var report struct {
		TotalBytes int64
		Assets     []json.RawMessage
	}
	if json.Unmarshal(output, &report) != nil || report.TotalBytes <= 0 || len(report.Assets) == 0 {
		t.Fatal("production audit returned no asset measurements", string(output))
	}
	for _, kind := range []string{"gzip", "brotli", "raw"} {
		t.Run(kind, func(t *testing.T) {
			copyRoot := filepath.Join(t.TempDir(), "copied runtime with spaces")
			if err := os.MkdirAll(copyRoot, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(copyRoot, "build.json"), body, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(filepath.Join(copyRoot, "assets"), os.DirFS(filepath.Join(source, "assets"))); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(copyRoot, "assets", "runtime", name)
			data := []byte("invalid compressed representation")
			switch kind {
			case "gzip":
				target += ".gz"
			case "brotli":
				target += ".br"
			case "raw":
				data, err = os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				data = append(data, []byte("changed")...)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal("production representation is missing", err)
			}
			if err := os.WriteFile(target, data, 0644); err != nil {
				t.Fatal(err)
			}
			output, err := run(filepath.Join(copyRoot, "build.json"))
			if err == nil || !strings.Contains(strings.ToLower(string(output)), "sidecar") {
				t.Fatalf("%s corruption did not fail canonical representation checks: %v: %s", kind, err, output)
			}
		})
	}
}
