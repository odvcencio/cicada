package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/isolatetest"
)

// A declared link may be loaded under its logical name, but a write must not
// follow it out of the project or onto a missing target.
func TestScoreWritersRefuseUnsafeDeclaredLinks(t *testing.T) {
	bin := buildCicadaForTest(t)
	for _, kind := range []string{"dangling-entry", "dangling-secondary", "outside-entry", "outside-secondary"} {
		for _, command := range []string{"apply-dry", "apply-write", "fix", "fmt", "studio"} {
			t.Run(kind+"/"+command, func(t *testing.T) {
				if command != "studio" {
					t.Parallel()
				}
				cfg := t.TempDir()
				root := t.TempDir()
				outside := filepath.Join(t.TempDir(), "outside.cicada")
				entry := "track bass acid { send_a = 0.2 }\nfx room delay {}\nscene main { bass=pulse }\nsong { main }\n"
				part := "pattern pulse acid steps=4 { 1 . 5 . }\n"
				opened := "main.cicada"
				if kind == "dangling-secondary" || kind == "outside-secondary" {
					opened = "parts.cicada"
				}
				files := map[string]string{
					"cicada.mod":   "project refuse\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n",
					"main.cicada":  entry,
					"parts.cicada": part,
				}
				for name, source := range files {
					if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
						t.Fatal(err)
					}
				}
				link := filepath.Join(root, opened)
				target := filepath.Join(t.TempDir(), "missing.cicada")
				if kind[:3] == "out" {
					if err := os.WriteFile(outside, []byte(files[opened]), 0600); err != nil {
						t.Fatal(err)
					}
					target = outside
				}
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				before := []byte(files[opened])
				after := append(bytes.Clone(before), []byte("// refused\n")...)
				var err error
				switch command {
				case "studio":
					isolatetest.Use(t, cfg)
				default:
					args := []string{"fix", link, "--all"}
					switch command {
					case "apply-dry", "apply-write":
						intents := filepath.Join(t.TempDir(), "intents.json")
						writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Revision: edits.Revision(before), Intents: []edits.Intent{&edits.ReplaceText{Source: string(after)}}})
						args = []string{"apply", link, intents}
						if command == "apply-write" {
							args = append(args, "--write")
						}
					case "fmt":
						args = []string{"fmt", "-w"}
					}
					cmd := commandWithEnv(bin, root, cfg, args...)
					var output []byte
					output, err = cmd.CombinedOutput()
					if err == nil && !(command == "apply-dry" && kind[:3] == "out") {
						t.Fatalf("%s accepted unsafe link:\n%s", command, output)
					}
					if err != nil && len(bytes.TrimSpace(output)) == 0 {
						t.Errorf("refusal has no message")
					}
				}
				if command == "studio" {
					s, openErr := newStudio(link)
					if openErr == nil {
						defer s.shutdown()
						response := studioCall(t, s.domainRoutes(), "/api/source", studioEdit{Revision: studioRevision(before), Source: string(after)})
						if response.Code == 200 {
							t.Errorf("Studio saved through unsafe link")
						}
					}
				}
				if got, err := os.Readlink(link); err != nil || got != target {
					t.Errorf("link changed: %q %v; want %q", got, err, target)
				}
				if kind[:3] == "out" {
					if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, before) {
						t.Errorf("outside target written: %q %v", got, err)
					}
				}
			})
		}
	}
}

func buildCicadaForTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("nice", "-n", "10", "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	return bin
}

func commandWithEnv(bin, dir, config string, args ...string) *exec.Cmd {
	command := exec.Command(bin, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), isolatetest.Env(config)...)
	return command
}
