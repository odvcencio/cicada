package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	local := "http://127.0.0.1:52341/__local/" + strings.Repeat("a", 26)
	if match := studioReadyLine.FindStringSubmatch("Cicada Studio: " + local); len(match) != 2 || match[1] != local {
		t.Fatal("desktop dropped the local startup capability")
	}
	for _, url := range []string{"http://127.0.0.1:52341/__local/short", "http://127.0.0.1:52341/other", "http://127.0.0.1:52341/@example.com"} {
		if studioReadyLine.MatchString("Cicada Studio: " + url) {
			t.Fatal("unexpected startup path accepted")
		}
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

func TestStudioEnvironmentKeepsStateInChosenDataFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Studio profile")
	got := studioEnvironment(filepath.Join(dir, "state.json"))
	if len(got) != 2 || got[0] != "CICADA_STATE_DIR="+dir || got[1] != "CICADA_AUDIO_SETTINGS_DIR=" {
		t.Fatalf("audio settings escaped the chosen profile: %q", got)
	}
	if got := studioEnvironment(""); len(got) != 0 {
		t.Fatalf("host without a data path changed the child environment: %q", got)
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
	if !q.request("a.cicada", "", false) {
		t.Fatal("the first request must start")
	}
	if q.request("b.cicada", "", false) || q.request("c.cicada", "", false) {
		t.Fatal("requests during a switch must wait")
	}
	if next := q.done(); next != "c.cicada" {
		t.Fatalf("done returned %q, want the latest request c.cicada", next)
	}
	if q.request("d.cicada", "", false) {
		t.Fatal("handoff must keep the worker reserved so a new Finder event waits")
	}
	if next := q.done(); next != "d.cicada" {
		t.Fatalf("new request was overtaken during handoff: got %q", next)
	}
	if next := q.done(); next != "" {
		t.Fatalf("nothing was requested during the second switch, got %q", next)
	}
	if !q.request("e.cicada", "", false) {
		t.Fatal("an idle queue must accept another switch")
	}
}

func TestOpenQueueReusesDuplicateStartupWithoutLosingDistinctRequest(t *testing.T) {
	var q openQueue
	if !q.request("first.cicada", "", false) {
		t.Fatal("startup did not reserve its score")
	}
	if q.request("first.cicada", "", false) || q.done() != "" {
		t.Fatal("duplicate initial Finder delivery restarted the score")
	}
	if !q.request("first.cicada", "", false) {
		t.Fatal("a failed startup must be retryable")
	}
	q.request("second.cicada", "", false)
	q.request("third.cicada", "", false)
	q.request("first.cicada", "", false)
	if next := q.done(); next != "third.cicada" {
		t.Fatalf("duplicate startup displaced the latest distinct request: %q", next)
	}
	q.request("third.cicada", "", false)
	if q.done() != "" {
		t.Fatal("a duplicate handoff restarted the score")
	}
}

func TestOpenQueueReusesLiveScoreButReloadAndFailureCanRestart(t *testing.T) {
	var q openQueue
	key := scorePathKey("song.cicada")
	if q.request("song.cicada", key, false) {
		t.Fatal("opening the live score must keep its current Session")
	}
	if !q.request("song.cicada", key, true) {
		t.Fatal("explicit Reload must restart the live score")
	}
	q.request("song.cicada", key, true)
	q.request("song.cicada", key, false)
	if q.done() != "song.cicada" || q.done() != "" {
		t.Fatal("an explicit reload during startup must remain queued")
	}
	if !q.request("song.cicada", "", false) {
		t.Fatal("failed or exited sidecars must allow the same score to retry")
	}
}

func TestOpenQueueComparesFinderAliasesWithoutChangingLogicalPaths(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.cicada")
	alias := filepath.Join(dir, "alias.cicada")
	if err := os.WriteFile(real, []byte("title \"test\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	var q openQueue
	if !q.request(alias, "", false) || q.request(real, "", false) || q.done() != "" {
		t.Fatal("Finder's resolved path duplicated an in-flight alias")
	}
	if q.request(alias, scorePathKey(real), false) {
		t.Fatal("an alias restarted the live score")
	}
	q.request("other.cicada", "", false)
	q.request(alias, "", false)
	if next := q.done(); next != alias {
		t.Fatalf("opening lost the logical path used for project assets: %q", next)
	}
}

func TestNoScoreStartupCreatesNewProjectAndArgsOmitEmptyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; desktop startup is exercised by the Windows smoke")
	}
	dir := t.TempDir()
	cicada := filepath.Join(dir, "cicada")
	script := `#!/bin/sh
[ "$1" = new ] || exit 1
mkdir "$2" || exit 1
printf 'cicada 2\ntrack bass acid {}\npattern p {1 .}\nscene main {bass=p}\nsong {main}\n' > "$2/main.cicada"
printf 'project %s\ncicada 2\n' "$2" > "$2/cicada.mod"
`
	if err := os.WriteFile(cicada, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(dir, "Documents", "Cicada")
	first, err := resolveInitialScore(nil, hostState{LastFile: filepath.Join(dir, "gone.cicada")}, cicada, parent)
	if err != nil || !isFile(first) || first != filepath.Join(parent, "untitled-1", "main.cicada") {
		t.Fatalf("new startup: %q %v", first, err)
	}
	second, err := newProject(cicada, parent, nextProjectName(parent))
	if err != nil || second != filepath.Join(parent, "untitled-2", "main.cicada") {
		t.Fatalf("File > New: %q %v", second, err)
	}
	remembered, err := resolveInitialScore(nil, hostState{LastFile: first}, cicada, parent)
	if err != nil || remembered != first {
		t.Fatal("last project was not restored")
	}
	for _, arg := range studioArgs("", "null") {
		if arg == "" {
			t.Fatal("empty sidecar argument")
		}
	}
}
