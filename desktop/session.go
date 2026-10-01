// Command cicada-studio is the Cicada Studio desktop app. It runs
// `cicada studio` as a sidecar process, which keeps the native Tymbal audio
// engine in Go, and shows the Studio page in a GoSX desktop window with
// native File menus.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// studioReadyLine matches the address line `cicada studio` prints once it
// listens.
var studioReadyLine = regexp.MustCompile(`^Cicada Studio: (http://127\.0\.0\.1:\d+/)`)

const maxRecentFiles = 8

// hostState is the small JSON file the host keeps between launches.
type hostState struct {
	LastFile string   `json:"last_file,omitempty"`
	Recent   []string `json:"recent,omitempty"`
}

func loadState(path string) hostState {
	var state hostState
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if json.Unmarshal(data, &state) != nil {
		return hostState{}
	}
	return state
}

func saveState(path string, state hostState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// remember moves file to the front of the recent list.
func (s *hostState) remember(file string) {
	s.LastFile = file
	recent := []string{file}
	for _, existing := range s.Recent {
		if !samePath(existing, file) && len(recent) < maxRecentFiles {
			recent = append(recent, existing)
		}
	}
	s.Recent = recent
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// findCicada locates the cicada executable: an explicit path first, then
// next to this program (the installed layout), then PATH.
func findCicada(explicit, selfPath string) (string, error) {
	if explicit != "" {
		if isFile(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("cicada executable %q not found", explicit)
	}
	name := "cicada"
	if runtime.GOOS == "windows" {
		name = "cicada.exe"
	}
	if selfPath != "" {
		dir := filepath.Dir(selfPath)
		for _, candidate := range []string{filepath.Join(dir, name), filepath.Join(dir, "bin", name)} {
			if isFile(candidate) {
				return candidate, nil
			}
		}
	}
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	return "", errors.New("cicada executable not found next to Cicada Studio or on PATH")
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// studioArgs builds the `cicada studio` command line for a score.
func studioArgs(score, audio string) []string {
	args := []string{"studio", score, "--listen", "127.0.0.1:0"}
	if audio != "" {
		args = append(args, "--audio", audio)
	}
	return args
}

// initialScore picks the score to open at launch: a .cicada argument, then
// the last file if it still exists. Empty means "create a new project".
func initialScore(args []string, state hostState) string {
	for _, arg := range args {
		if strings.EqualFold(filepath.Ext(arg), ".cicada") && isFile(arg) {
			if abs, err := filepath.Abs(arg); err == nil {
				return abs
			}
			return arg
		}
	}
	if state.LastFile != "" && isFile(state.LastFile) {
		return state.LastFile
	}
	return ""
}

// nextProjectName returns the first "untitled-N" name not used in dir.
func nextProjectName(dir string) string {
	for n := 1; ; n++ {
		name := fmt.Sprintf("untitled-%d", n)
		if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
	}
}

// newProject runs `cicada new <name>` in parent and returns the new score.
func newProject(cicada, parent, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command(cicada, "new", name)
	cmd.Dir = parent
	hideConsole(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("cicada new %s: %v: %s", name, err, strings.TrimSpace(string(output)))
	}
	return filepath.Join(parent, name, "main.cicada"), nil
}

// saveCopyWithAssets uses the core collector so desktop Save As retains audio,
// historical passes and recovery dependencies before exposing the new score.
func saveCopyWithAssets(cicada, score, target string) error {
	cmd := exec.Command(cicada, "save-as", score, target)
	hideConsole(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cicada save-as: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func windowTitle(score string) string {
	if score == "" {
		return "Cicada Studio"
	}
	name := strings.TrimSuffix(filepath.Base(score), filepath.Ext(score))
	if name == "main" {
		name = filepath.Base(filepath.Dir(score))
	}
	return name + " - Cicada Studio"
}

// openQueue lets one score switch run at a time and keeps the latest request that
// arrives while a switch is running, so a menu click or a second launch is not lost.
type openQueue struct {
	mu      sync.Mutex
	busy    bool
	pending string
}

// request reports whether the caller may start the switch now. If a switch is
// already running, the score is kept and false is returned.
func (q *openQueue) request(score string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.busy {
		q.pending = score
		return false
	}
	q.busy = true
	return true
}

// done ends the running switch and returns the latest score requested meanwhile,
// or an empty string. The caller then opens it.
func (q *openQueue) done() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.busy = false
	next := q.pending
	q.pending = ""
	return next
}
