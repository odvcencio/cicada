package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
)

func secondarySceneStudio(t *testing.T, dir string) (*studio, string, []byte) {
	t.Helper()
	path := filepath.Join(dir, "main.cicada")
	entry := "cicada 2\ntrack vox audio {}\ntrack bass acid {}\nsong { main }\n"
	part := []byte("// retain the arrangement file\r\npattern pulse acid steps=4 { 1 . 5 . }\r\nscene main { bass = pulse vox = off // keep binding comment\r\n}\r\n")
	for name, content := range map[string][]byte{
		"main.cicada": []byte(entry), "parts.cicada": part,
		"cicada.mod": []byte("project scene-sources\ncicada 2\nentry \"main.cicada\"\nsource \"parts.cicada\"\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.shutdown() })
	if err := s.openTakes(); err != nil {
		t.Fatal(err)
	}
	return s, entry, part
}

func TestSecondarySceneTakeCrashChild(t *testing.T) {
	stage := os.Getenv("CICADA_SCENE_CRASH_STAGE")
	if stage == "" {
		return
	}
	dir := os.Getenv("CICADA_SCENE_CRASH_DIR")
	s, _, _ := secondarySceneStudio(t, dir)
	id := captureTestTake(t, s)
	if os.Getenv("CICADA_SCENE_CRASH_RETAINED") == "1" {
		for _, selected := range []string{id, captureTestTake(t, s)} {
			source, _ := os.ReadFile(s.path)
			if err := s.commitTake(selected, studioRevision(source), nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte(id), 0600); err != nil {
		t.Fatal(err)
	}
	s.takes.Boundary = func(at takejournal.Stage) {
		if string(at) == stage {
			os.Exit(73)
		}
	}
	source, _ := os.ReadFile(s.path)
	if err := s.commitTake(id, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary not reached")
}

func TestSecondarySceneTakeCrashRecovery(t *testing.T) {
	for _, retained := range []bool{false, true} {
		for _, stage := range []takejournal.Stage{takejournal.Prepared, takejournal.SourceStaged, takejournal.SourceSwapped, takejournal.AuxiliarySwapped, takejournal.Source, takejournal.Committed} {
			for _, external := range []bool{false, true} {
				if external && stage != takejournal.SourceSwapped {
					continue
				}
				t.Run(fmt.Sprintf("retained=%v/%s/external=%v", retained, stage, external), func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					child := exec.Command(os.Args[0], "-test.run=^TestSecondarySceneTakeCrashChild$")
					child.Env = append(os.Environ(), "CICADA_SCENE_CRASH_DIR="+dir, "CICADA_SCENE_CRASH_STAGE="+string(stage))
					if retained {
						child.Env = append(child.Env, "CICADA_SCENE_CRASH_RETAINED=1")
					}
					output, err := child.CombinedOutput()
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 73 {
						t.Fatalf("child did not reach %s: %v\n%s", stage, err, output)
					}
					partPath := filepath.Join(dir, "parts.cicada")
					part, _ := os.ReadFile(partPath)
					if external {
						part = append(part, []byte("// external arrangement edit\r\n")...)
						if err := os.WriteFile(partPath, part, 0600); err != nil {
							t.Fatal(err)
						}
					}
					s, err := newStudio(filepath.Join(dir, "main.cicada"))
					if err != nil {
						t.Fatal(err)
					}
					id, _ := os.ReadFile(filepath.Join(dir, "target"))
					take, err := s.takes.Get(string(id))
					if err != nil {
						t.Fatal(err)
					}
					after, _ := os.ReadFile(partPath)
					if external {
						if take.Stage != takejournal.Conflict || !bytes.Equal(part, after) || len(take.Files) == 0 {
							t.Fatal("recovery overwrote an external edit or lost the pending scene")
						}
					} else if take.Stage != takejournal.Committed || !bytes.Contains(after, []byte("vox = "+string(id)+"-clip // keep binding comment\r\n")) {
						t.Fatalf("incomplete recovery at %s: %s\n%s", stage, take.Stage, after)
					}
					projection, _ := json.Marshal(s.takeProjection())
					if bytes.Contains(projection, []byte(`"candidate"`)) || bytes.Contains(projection, []byte(`"files"`)) {
						t.Fatal("private prepared source leaked into take polling")
					}
					s.shutdown()
					again, err := newStudio(s.path)
					if err != nil {
						t.Fatal(err)
					}
					defer again.shutdown()
					second, _ := os.ReadFile(partPath)
					if !bytes.Equal(after, second) {
						t.Fatal("a second restart replayed a selection")
					}
				})
			}
		}
	}
}

func TestTakeSelectionInSecondaryScene(t *testing.T) {
	dir := t.TempDir()
	s, entry, part := secondarySceneStudio(t, dir)
	partsPath := filepath.Join(dir, "parts.cicada")
	first := captureTestTake(t, s)
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: first, Revision: studioRevision([]byte(entry))})
	if response.Code != http.StatusOK {
		t.Fatalf("secondary scene cannot select a take: %d %s", response.Code, response.Body.String())
	}
	selected, _ := os.ReadFile(s.path)
	selectedPart, _ := os.ReadFile(partsPath)
	wantPart := bytes.Replace(part, []byte("vox = off"), []byte("vox = "+first+"-clip"), 1)
	if !bytes.Equal(selectedPart, wantPart) || !strings.Contains(string(selected), "asset "+first) {
		t.Fatal("selection must retain scene comments and newlines and put take declarations in the entry file")
	}
	second := captureTestTake(t, s)
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: second, Revision: studioRevision(selected)})
	if response.Code != http.StatusOK {
		t.Fatalf("second selection: %d %s", response.Code, response.Body.String())
	}
	both, _ := os.ReadFile(s.path)
	// Selecting an already published take changes only the secondary scene.
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: first, Revision: studioRevision(both)})
	if response.Code != http.StatusOK {
		t.Fatalf("retained selection: %d %s", response.Code, response.Body.String())
	}
	again, _ := os.ReadFile(s.path)
	againPart, _ := os.ReadFile(partsPath)
	if !bytes.Equal(both, again) || !bytes.Equal(againPart, wantPart) {
		t.Fatal("switching retained takes must change only the scene's owning file")
	}
	response = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(again)})
	undone, _ := os.ReadFile(partsPath)
	if response.Code != http.StatusOK || !bytes.Equal(undone, bytes.Replace(wantPart, []byte(first+"-clip"), []byte(second+"-clip"), 1)) {
		t.Fatal("Undo did not restore the previous secondary-file selection", response.Body.String())
	}
	response = studioCall(t, s.routes(), "/api/redo", studioEdit{Revision: studioRevision(again)})
	redone, _ := os.ReadFile(partsPath)
	if response.Code != http.StatusOK || !bytes.Equal(redone, wantPart) {
		t.Fatal("Redo did not restore the secondary-file selection", response.Body.String())
	}
}

func TestSceneEditsRejectUnseenSecondaryWrites(t *testing.T) {
	for _, during := range []bool{false, true} {
		for _, kind := range []string{"bind", "create", "automation", "take"} {
			t.Run(fmt.Sprintf("%s/during=%v", kind, during), func(t *testing.T) {
				dir := t.TempDir()
				s, entry, part := secondarySceneStudio(t, dir)
				seen, err := project.ReadScoreRevision(s.path)
				if err != nil {
					t.Fatal(err)
				}
				request := studioEdit{Revision: studioRevision([]byte(entry)), SourceRevision: seen, Scene: "main", Track: "bass", Action: kind, Pattern: "off"}
				route := "/api/pattern"
				switch kind {
				case "create":
					request.NewName, request.Length = "idea", 8
				case "automation":
					route, request.Action, request.Path, request.Value = "/api/automation", "automation-set", "bass.cutoff", json.RawMessage(`900`)
				case "take":
					route, request.Action, request.TakeID = "/api/takes", "select", captureTestTake(t, s)
				}
				external := append(bytes.Clone(part), []byte("// another editor's arrangement\r\n")...)
				change := func() {
					if err := os.WriteFile(filepath.Join(dir, "parts.cicada"), external, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if during {
					s.afterStage = change
				} else {
					change()
				}
				response := studioCall(t, s.routes(), route, request)
				if response.Code != http.StatusConflict {
					t.Fatalf("unseen source accepted: %d %s", response.Code, response.Body.String())
				}
				current, _ := os.ReadFile(s.path)
				currentPart, _ := os.ReadFile(filepath.Join(dir, "parts.cicada"))
				if string(current) != entry || !bytes.Equal(currentPart, external) {
					t.Fatal("refused edit changed source bytes")
				}
			})
		}
	}
}
