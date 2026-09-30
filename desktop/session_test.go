package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStudioReadyLine(t *testing.T) {
	match := studioReadyLine.FindStringSubmatch("Cicada Studio: http://127.0.0.1:52341/")
	if len(match) != 2 || match[1] != "http://127.0.0.1:52341/" {
		t.Fatalf("match = %q", match)
	}
	if studioReadyLine.MatchString("Cicada Studio: http://example.com:80/") {
		t.Fatal("non-loopback address matched")
	}
}

func TestStudioArgs(t *testing.T) {
	got := studioArgs("song.cicada", "")
	want := []string{"studio", "song.cicada", "--listen", "127.0.0.1:0"}
	if len(got) != len(want) {
		t.Fatalf("args = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %q, want %q", got, want)
		}
	}
	if got := studioArgs("song.cicada", "null"); got[len(got)-2] != "--audio" || got[len(got)-1] != "null" {
		t.Fatalf("args with audio = %q", got)
	}
}

func TestStateRememberKeepsRecentOrderAndLimit(t *testing.T) {
	var state hostState
	for i := 0; i < maxRecentFiles+3; i++ {
		state.remember(filepath.Join("scores", string(rune('a'+i))+".cicada"))
	}
	state.remember(filepath.Join("scores", "c.cicada"))
	if state.LastFile != filepath.Join("scores", "c.cicada") || state.Recent[0] != state.LastFile {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Recent) != maxRecentFiles {
		t.Fatalf("recent has %d entries, want %d", len(state.Recent), maxRecentFiles)
	}
	seen := map[string]bool{}
	for _, file := range state.Recent {
		if seen[file] {
			t.Fatalf("duplicate recent entry %q", file)
		}
		seen[file] = true
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	state := hostState{LastFile: "a.cicada", Recent: []string{"a.cicada", "b.cicada"}}
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	got := loadState(path)
	if got.LastFile != "a.cicada" || len(got.Recent) != 2 {
		t.Fatalf("loaded %+v", got)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadState(path); got.LastFile != "" || got.Recent != nil {
		t.Fatalf("corrupt state loaded as %+v", got)
	}
}

func TestInitialScore(t *testing.T) {
	dir := t.TempDir()
	score := filepath.Join(dir, "song.cicada")
	last := filepath.Join(dir, "last.cicada")
	for _, file := range []string{score, last} {
		if err := os.WriteFile(file, []byte("title \"x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := initialScore([]string{"--flag", score}, hostState{LastFile: last}); got != score {
		t.Fatalf("argument score = %q", got)
	}
	if got := initialScore(nil, hostState{LastFile: last}); got != last {
		t.Fatalf("last score = %q", got)
	}
	if got := initialScore([]string{filepath.Join(dir, "missing.cicada")}, hostState{LastFile: filepath.Join(dir, "gone.cicada")}); got != "" {
		t.Fatalf("missing files gave %q", got)
	}
}

func TestNextProjectName(t *testing.T) {
	dir := t.TempDir()
	if got := nextProjectName(dir); got != "untitled-1" {
		t.Fatalf("first name = %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "untitled-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := nextProjectName(dir); got != "untitled-2" {
		t.Fatalf("second name = %q", got)
	}
}

func TestSaveCopyBringsProjectManifest(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	score := filepath.Join(source, "main.cicada")
	if err := os.WriteFile(score, []byte("title \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "cicada.mod"), []byte("project x\ncicada 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(target, "copy.cicada")
	if err := saveCopy(score, copyPath); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(copyPath); err != nil || string(data) != "title \"x\"\n" {
		t.Fatalf("copy = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "cicada.mod")); err != nil || string(data) != "project x\ncicada 2\n" {
		t.Fatalf("manifest = %q, %v", data, err)
	}
	// An existing manifest at the target is kept.
	if err := os.WriteFile(filepath.Join(target, "cicada.mod"), []byte("project keep\ncicada 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveCopy(score, filepath.Join(target, "second.cicada")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "cicada.mod")); string(data) != "project keep\ncicada 2\n" {
		t.Fatalf("target manifest overwritten: %q", data)
	}
}

func TestFindCicada(t *testing.T) {
	dir := t.TempDir()
	name := "cicada"
	if runtime.GOOS == "windows" {
		name = "cicada.exe"
	}
	sibling := filepath.Join(dir, name)
	if err := os.WriteFile(sibling, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "cicada-studio")
	if got, err := findCicada("", self); err != nil || got != sibling {
		t.Fatalf("sibling = %q, %v", got, err)
	}
	if _, err := findCicada(filepath.Join(dir, "missing"), self); err == nil {
		t.Fatal("missing explicit path accepted")
	}
}

func TestWindowTitle(t *testing.T) {
	if got := windowTitle(filepath.Join("songs", "night-drive", "main.cicada")); got != "night-drive - Cicada Studio" {
		t.Fatalf("project title = %q", got)
	}
	if got := windowTitle(filepath.Join("songs", "loop.cicada")); got != "loop - Cicada Studio" {
		t.Fatalf("score title = %q", got)
	}
}

func TestOpenQueueKeepsTheLatestRequestDuringASwitch(t *testing.T) {
	var q openQueue
	if !q.request("a.cicada") {
		t.Fatal("the first request must start")
	}
	if q.request("b.cicada") || q.request("c.cicada") {
		t.Fatal("requests during a switch must wait")
	}
	if next := q.done(); next != "c.cicada" {
		t.Fatalf("done returned %q, want the latest request c.cicada", next)
	}
	if !q.request("c.cicada") {
		t.Fatal("after done the queue must accept a new switch")
	}
	if next := q.done(); next != "" {
		t.Fatalf("nothing was requested during the second switch, got %q", next)
	}
}
