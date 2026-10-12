package main

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

// Exercise the declared entry and a declared secondary source independently:
// neither loading nor editor overrides may substitute the link's target name.
func TestScoreWriterLogicalIdentity(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("nice", "-n", "10", "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, kind := range []string{"entry-link", "secondary-link", "project-link", "regular"} {
		for _, explicit := range []bool{false, true} {
			manifestKind := map[bool]string{false: "implicit", true: "explicit"}[explicit]
			for _, command := range []string{"apply", "fix", "fmt", "studio"} {
				t.Run(kind+"/"+manifestKind+"/"+command, func(t *testing.T) {
					t.Setenv("XDG_CONFIG_HOME", t.TempDir())
					root := t.TempDir()
					entry := "track bass acid { send_a = 0.2 }\nfx room delay {}\nscene main { bass=pulse }\nsong { main }\n"
					part := "pattern pulse acid steps=4 { 1 . 5 . }\n"
					manifest := "project identity\ncicada 1\n"
					if explicit {
						manifest += "entry \"main.cicada\"\nsource \"parts.cicada\"\n"
					} else {
						entry += part
						part = entry
					}
					for name, source := range map[string]string{"cicada.mod": manifest, "main.cicada": entry, "parts.cicada": part} {
						if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
							t.Fatal(err)
						}
					}
					opened := "main.cicada"
					if kind == "secondary-link" {
						opened = "parts.cicada"
					}
					physical := filepath.Join(root, opened)
					logicalRoot := root
					links := map[string]string{}
					if kind == "entry-link" || kind == "secondary-link" {
						physical += ".target"
						link := filepath.Join(root, opened)
						if err := os.Rename(link, physical); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(filepath.Base(physical), link); err != nil {
							t.Fatal(err)
						}
						links[link] = filepath.Base(physical)
					} else if kind == "project-link" {
						logicalRoot = filepath.Join(t.TempDir(), "alias")
						if err := os.Symlink(root, logicalRoot); err != nil {
							t.Fatal(err)
						}
						links[logicalRoot] = root
					}
					logical := filepath.Join(logicalRoot, opened)
					check := func() {
						t.Helper()
						if output, err := runCreationCommand(bin, logicalRoot, "check", logical); err != nil {
							t.Fatalf("check rejected declared score: %v\n%s", err, output)
						}
					}
					check()
					before, err := os.ReadFile(physical)
					if err != nil {
						t.Fatal(err)
					}
					after := append(bytes.Clone(before), []byte("// saved under declared identity\n")...)
					switch command {
					case "studio":
						s, err := newStudio(logical)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = s.shutdown() })
						if s.path != logical {
							t.Errorf("Studio lost declared identity: %s; want %s", s.path, logical)
						}
						response := studioCall(t, s.domainRoutes(), "/api/source", studioEdit{Revision: studioRevision(before), Source: string(after)})
						if response.Code != http.StatusOK {
							t.Fatalf("HTTP save: %d %s", response.Code, response.Body.String())
						}
					default:
						args := []string{command, logical}
						if command == "apply" {
							intents := filepath.Join(root, "intents.json")
							writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Revision: edits.Revision(before), Intents: []edits.Intent{&edits.ReplaceText{Source: string(after)}}})
							args = append(args, intents, "--write")
						} else if command == "fmt" {
							args = append(args, "-w")
						} else if command == "fix" {
							args = append(args, "--all")
						}
						if output, err := runCreationCommand(bin, logicalRoot, args...); err != nil {
							t.Fatalf("%s: %v\n%s", command, err, output)
						}
						if command == "fmt" {
							var stdout, stderr bytes.Buffer
							if err := formatProject(logicalRoot, false, &stdout, &stderr); err != nil {
								t.Fatalf("project fmt: %v %s", err, stderr.String())
							}
							if logicalRoot != root && bytes.Contains(stdout.Bytes(), []byte(root)) {
								t.Errorf("project fmt printed physical names: %s", stdout.String())
							}
						}
					}
					got, err := os.ReadFile(physical)
					if err != nil || bytes.Equal(got, before) {
						t.Fatalf("physical target unchanged: %q %v", got, err)
					}
					if (command == "apply" || command == "studio") && !bytes.Equal(got, after) {
						t.Errorf("override missed declared file: %q; want %q", got, after)
					}
					for link, target := range links {
						if got, err := os.Readlink(link); err != nil || got != target {
							t.Errorf("link changed: %q %v; want %q", got, err, target)
						}
					}
					check()
					loaded, err := project.ReadSources(logical, nil)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, file := range loaded.Files {
						found = found || file.Path == logical
						if file.Path == physical && physical != logical {
							t.Errorf("loader exposed physical name: %s", file.Path)
						}
					}
					if !found {
						t.Error("loader lost declared filename")
					}
				})
			}
		}
	}
}

// A dry run reads under the declared name, prints a diff against it, and writes nothing.
func TestApplyDryRunKeepsDeclaredLink(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	source := "track bass acid {}\npattern p acid steps=1 { 1 }\nscene main { bass=p }\nsong { main }\n"
	for name, data := range map[string]string{"cicada.mod": "project dry\ncicada 1\nentry \"main.cicada\"\nsource \"parts.cicada\"\n", "main.cicada.target": source, "parts.cicada": "// part\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "main.cicada")
	if err := os.Symlink("main.cicada.target", link); err != nil {
		t.Fatal(err)
	}
	intents := filepath.Join(root, "intents.json")
	after := source + "// dry\n"
	writeApplyEnvelope(t, intents, edits.Envelope{Version: 1, Revision: edits.Revision([]byte(source)), Intents: []edits.Intent{&edits.ReplaceText{Source: after}}})
	var out bytes.Buffer
	if err := applyCommandWithHooks([]string{link, intents}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("+// dry")) {
		t.Errorf("no diff: %q", out.String())
	}
	if got, _ := os.ReadFile(filepath.Join(root, "main.cicada.target")); string(got) != source {
		t.Errorf("dry run wrote: %q", got)
	}
	if got, err := os.Readlink(link); err != nil || got != "main.cicada.target" {
		t.Errorf("link changed: %q %v", got, err)
	}
}
