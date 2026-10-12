package main

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/smf"
)

func TestCreatingCommandsDestinationParents(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "cicada")
	build := exec.Command("nice", "-n", "10", "go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	commands := []struct {
		name           string
		existingParent bool
		exclusive      bool
	}{
		{"save-as", false, true}, {"new", false, true}, {"lib-new", false, true},
		{"gen", true, false}, {"convert", true, false}, {"transcribe", true, true},
		{"import-midi", true, true}, {"lib-vendor", false, false}, {"lib-update", false, false},
	}
	for _, command := range commands {
		for _, kind := range []string{"missing-nested", "symlink-parent", "existing-parent"} {
			t.Run(command.name+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				creationCommandInputs(t, root)
				parent := filepath.Join(root, "exports", "nested")
				physical := parent
				if kind == "existing-parent" {
					if err := os.MkdirAll(parent, 0700); err != nil {
						t.Fatal(err)
					}
				} else if kind == "symlink-parent" {
					parent = filepath.Join(root, "exports")
					physical = filepath.Join(root, "actual")
					if err := os.Mkdir(physical, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("actual", parent); err != nil {
						t.Fatal(err)
					}
				}
				dir := root
				target := filepath.Join(parent, "copy.cicada")
				written := filepath.Join(physical, "copy.cicada")
				var args []string
				switch command.name {
				case "save-as":
					args = []string{"save-as", filepath.Join(root, "main.cicada"), target}
				case "new":
					// new accepts a project name, not an arbitrary destination.
					if kind != "missing-nested" {
						dir = parent
					} else {
						physical = root
					}
					written = filepath.Join(physical, "fresh", "main.cicada")
					args = []string{"new", "fresh"}
				case "lib-new":
					written = filepath.Join(physical, "library", "main.cicada")
					args = []string{"lib", "new", "demo/fresh", "--dir", filepath.Join(parent, "library")}
				case "gen":
					args = []string{"gen", "--seed", "4242", "--steps", "8", "-o", target}
				case "convert":
					written = filepath.Join(physical, "copy.json")
					args = []string{"convert", filepath.Join(root, "main.cicada"), "-o", filepath.Join(parent, "copy.json")}
				case "transcribe":
					args = []string{"transcribe", filepath.Join(root, "voice.wav"), "--tempo", "120", "--key", "a minor", "-o", target}
				case "import-midi":
					args = []string{"import-midi", filepath.Join(root, "idea.mid"), "-o", target}
				case "lib-vendor", "lib-update":
					creationLibraryInputs(t, root)
					if command.name == "lib-vendor" {
						if output, err := runCreationCommand(bin, dir, "lib", "update"); err != nil {
							t.Fatalf("pin input library: %v %s", err, output)
						}
						libraryParent := filepath.Join(root, "lib")
						if kind == "symlink-parent" {
							if err := os.Symlink("actual", libraryParent); err != nil {
								t.Fatal(err)
							}
						} else {
							physical = libraryParent
							if kind == "existing-parent" {
								if err := os.MkdirAll(filepath.Join(libraryParent, "demo"), 0700); err != nil {
									t.Fatal(err)
								}
							}
						}
						written = filepath.Join(physical, "demo", "tone", "tone.cicada")
						args = []string{"lib", "vendor"}
					} else {
						// Pin publication always uses the existing project root.
						if kind == "symlink-parent" {
							alias := filepath.Join(t.TempDir(), "project")
							if err := os.Symlink(root, alias); err != nil {
								t.Fatal(err)
							}
							dir = alias
						}
						written = filepath.Join(root, "cicada.sum")
						args = []string{"lib", "update"}
					}
				}
				var before map[string]noopFile
				if kind == "missing-nested" && command.existingParent {
					before = noopFiles(t, root)
				}
				output, err := runCreationCommand(bin, dir, args...)
				if kind == "missing-nested" && command.existingParent {
					if err == nil || !strings.HasPrefix(string(output), "open ") || !strings.Contains(string(output), "no such file or directory") {
						t.Errorf("missing-parent error changed: %v %s", err, output)
					}
					if !reflect.DeepEqual(before, noopFiles(t, root)) {
						t.Error("refused command wrote project files")
					}
					if _, err := os.Stat(filepath.Join(root, "exports")); !os.IsNotExist(err) {
						t.Errorf("refused command created parents: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("creating command: %v %s", err, output)
				}
				data, err := os.ReadFile(written)
				if err != nil || len(data) == 0 {
					t.Fatalf("physical destination: %q %v", data, err)
				}
				if command.name == "save-as" {
					source, _ := os.ReadFile(filepath.Join(root, "main.cicada"))
					if !bytes.Equal(data, source) {
						t.Error("Save As changed source bytes")
					}
				}
				if command.exclusive {
					if output, err := runCreationCommand(bin, dir, args...); err == nil {
						t.Errorf("occupied destination accepted: %s", output)
					}
					if got, err := os.ReadFile(written); err != nil || !bytes.Equal(data, got) {
						t.Errorf("occupied destination changed: %q %v", got, err)
					}
				}
				if kind == "symlink-parent" {
					if got, err := os.Readlink(parent); err != nil || got != "actual" {
						t.Errorf("parent link changed: %q %v", got, err)
					}
				}
			})
		}
	}
}

func runCreationCommand(bin, dir string, args ...string) ([]byte, error) {
	command := exec.Command(bin, args...)
	command.Dir = dir
	return command.CombinedOutput()
}

func creationCommandInputs(t *testing.T, root string) {
	t.Helper()
	for name, source := range map[string]string{"main.cicada": "cicada 2\n" + studioScore, "cicada.mod": "project creation\ncicada 2\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const rate = 16000
	pcm := make([]float32, rate)
	for i := rate / 10; i < rate*8/10; i++ {
		pcm[i] = float32(.2 * math.Sin(2*math.Pi*220*float64(i)/rate))
	}
	if err := os.WriteFile(filepath.Join(root, "voice.wav"), recording.EncodeWAV(pcm, rate), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "idea.mid"))
	if err != nil {
		t.Fatal(err)
	}
	err = smf.Encode(smf.File{PPQ: 960, TempoMicros: 500000, Tracks: []smf.TrackChunk{{Notes: []smf.Note{{Tick: 0, Dur: 176, Note: 60, Vel: 64}}}}}, file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("MIDI fixture: %v %v", err, closeErr)
	}
}

func creationLibraryInputs(t *testing.T, root string) {
	t.Helper()
	user := t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	for name, source := range map[string]string{
		"demo/tone/cicada.mod":  "library demo/tone\ncicada 2\nsource \"tone.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
		"demo/tone/tone.cicada": "instrument glass { voice mono { out = sine(pitch) * env(gate,90ms) } }\npattern melody { 1 . 5 . }\n",
	} {
		path := filepath.Join(user, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.cicada"), []byte("import \"demo/tone\"\ntrack lead tone.glass {}\nscene main { lead=tone.melody }\nsong { main }\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
