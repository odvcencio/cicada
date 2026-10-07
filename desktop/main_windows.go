//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"m31labs.dev/gosx/desktop"
	"m31labs.dev/gosx/desktop/sidecar"
)

const (
	appID           = "dev.cicada.studio"
	backgroundColor = "#0f1411"
)

var buildVersion = "dev"

type host struct {
	mu        sync.Mutex
	app       *desktop.App
	cicada    string
	audio     string
	logOut    io.Writer
	statePath string
	state     hostState
	score     string
	proc      *sidecar.Process
	queue     openQueue
	closing   bool

	smokeOut  string
	smokeHold time.Duration
	started   time.Time
	smoke     map[string]any
}

func main() {
	if err := run(); err != nil {
		showError("Cicada Studio could not start", err.Error())
		os.Exit(1)
	}
}

func run() error {
	started := time.Now()
	flags := flag.NewFlagSet("cicada-studio", flag.ContinueOnError)
	cicadaPath := flags.String("cicada", "", "path to cicada.exe (default: next to this program, then PATH)")
	audio := flags.String("audio", "", "audio backend passed to cicada studio: tymbal, oto, or null")
	dataDir := flags.String("data-dir", "", "folder for settings, logs, and the WebView2 profile")
	mute := flags.Bool("mute", false, "mute the Studio page (tests)")
	devTools := flags.Bool("devtools", false, "enable developer tools (F12)")
	smokeOut := flags.String("smoke-out", "", "write a startup report to this JSON file and exit once Studio loads (tests)")
	smokeHold := flags.Duration("smoke-hold", 1500*time.Millisecond, "with --smoke-out, how long to keep Studio open after it loads")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if *dataDir == "" {
		base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
		if err != nil {
			return err
		}
		*dataDir = filepath.Join(base, "Cicada Studio")
	}
	if err := os.MkdirAll(filepath.Join(*dataDir, "logs"), 0o755); err != nil {
		return err
	}
	logFile, err := openLog(filepath.Join(*dataDir, "logs", "studio.log"))
	if err != nil {
		return err
	}
	defer logFile.Close()
	self, _ := os.Executable()
	cicada, err := findCicada(*cicadaPath, self)
	if err != nil {
		return err
	}
	h := &host{
		cicada:    cicada,
		audio:     *audio,
		logOut:    logFile,
		statePath: filepath.Join(*dataDir, "state.json"),
		smokeOut:  *smokeOut,
		smokeHold: *smokeHold,
		started:   started,
		smoke:     map[string]any{},
	}
	h.state = loadState(h.statePath)
	score, err := resolveInitialScore(flags.Args(), h.state, cicada, filepath.Join(documentsDir(), "Cicada"))
	if err != nil {
		return err
	}

	h.app, err = desktop.New(desktop.Options{
		Title:            windowTitle(score),
		Width:            1280,
		Height:           840,
		AppID:            appID,
		Version:          buildVersion,
		HTML:             statusPage("Starting Cicada Studio", filepath.Base(score)),
		BackgroundColor:  backgroundColor,
		UserDataDir:      filepath.Join(*dataDir, "WebView2"),
		MuteAudio:        *mute,
		DevTools:         *devTools,
		SingleInstance:   *smokeOut == "",
		OnSecondInstance: h.onSecondInstance,
		OnNavigationCompleted: func(event desktop.NavigationCompleted) {
			h.onNavigationCompleted(event)
		},
		// Start the audio engine once this process owns the window, while
		// WebView2 starts. Starting it earlier would also start it in a
		// second launch that only forwards its file and exits.
		OnWindowCreated: func(*desktop.Window) { go h.open(score) },
		OnClose:         h.shutdown,
	})
	if err != nil {
		return err
	}
	if err := h.app.SetMinSize(800, 560); err != nil {
		return err
	}
	if err := h.app.SetMenuBar(h.menu()); err != nil {
		return err
	}
	// Installed copies (Setup writes install.json beside the executable)
	// offer themselves for .cicada files in Explorer's "Open with" list.
	// The per-user registration refreshes the executable path each launch.
	if h.smokeOut == "" && isFile(filepath.Join(filepath.Dir(self), "install.json")) {
		if err := h.app.RegisterFileType(".cicada", "", "Cicada score"); err != nil {
			fmt.Fprintf(h.logOut, "host: register .cicada: %v\n", err)
		}
	}
	if h.smokeOut != "" {
		go func() {
			time.Sleep(60 * time.Second)
			h.recordSmoke("timeout", true)
			_ = h.app.Close()
		}()
	}
	runErr := h.app.Run()
	h.shutdown()
	if h.smokeOut != "" {
		h.writeSmoke()
	}
	return runErr
}

// open stops the current Studio process, starts one for score, and shows it.
func (h *host) open(score string) {
	h.mu.Lock()
	closing := h.closing
	h.mu.Unlock()
	if closing || !h.queue.request(score) {
		return
	}
	h.mu.Lock()
	previous := h.proc
	h.proc = nil
	h.mu.Unlock()
	defer func() {
		if next := h.queue.done(); next != "" {
			go h.open(next)
		}
	}()

	if previous != nil {
		_ = previous.Stop(3 * time.Second)
		_ = h.app.SetHTML(statusPage("Opening", filepath.Base(score)))
	}
	fmt.Fprintf(h.logOut, "%s host: starting %s %s\n", time.Now().Format(time.RFC3339), h.cicada, strings.Join(studioArgs(score, h.audio), " "))
	sidecarStart := time.Now()
	proc, err := sidecar.Start(context.Background(), sidecar.Options{
		Path:         h.cicada,
		Args:         studioArgs(score, h.audio),
		Dir:          filepath.Dir(score),
		Output:       h.logOut,
		ReadyLine:    studioReadyLine,
		ReadyTimeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(h.logOut, "%s host: studio failed: %v\n", time.Now().Format(time.RFC3339), err)
		_ = h.app.SetHTML(errorPage(filepath.Base(score), err))
		h.recordSmoke("sidecarError", err.Error())
		return
	}
	h.recordSmoke("sidecarReadyMs", time.Since(sidecarStart).Milliseconds())
	h.appendSmoke("opened", score)
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		_ = proc.Stop(time.Second)
		return
	}
	h.proc = proc
	h.score = score
	h.state.remember(score)
	state := h.state
	h.mu.Unlock()
	if err := saveState(h.statePath, state); err != nil {
		fmt.Fprintf(h.logOut, "host: save state: %v\n", err)
	}
	_ = h.app.SetTitle(windowTitle(score))
	if err := h.app.Navigate(proc.Ready()); err != nil {
		fmt.Fprintf(h.logOut, "host: navigate: %v\n", err)
	}
	go h.watch(proc, score)
}

// watch shows an error page if Studio exits while it is the current process.
func (h *host) watch(proc *sidecar.Process, score string) {
	err := proc.Wait()
	h.mu.Lock()
	current := h.proc == proc && !h.closing
	if current {
		h.proc = nil
	}
	h.mu.Unlock()
	if current {
		if err == nil {
			err = errors.New("cicada studio exited")
		}
		_ = h.app.SetHTML(errorPage(filepath.Base(score), err))
	}
}

func (h *host) shutdown() {
	h.mu.Lock()
	h.closing = true
	proc := h.proc
	h.proc = nil
	h.mu.Unlock()
	if proc != nil {
		_ = proc.Stop(2 * time.Second)
	}
}

func (h *host) currentScore() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.score
}

func (h *host) menu() desktop.Menu {
	h.mu.Lock()
	recent := append([]string(nil), h.state.Recent...)
	h.mu.Unlock()
	recentItems := []desktop.MenuItem{}
	for i, file := range recent {
		file := file
		recentItems = append(recentItems, desktop.MenuItem{
			ID:      fmt.Sprintf("file.recent.%d", i),
			Label:   fmt.Sprintf("&%d %s", i+1, file),
			OnClick: func() { go h.open(file) },
		})
	}
	if len(recentItems) == 0 {
		recentItems = append(recentItems, desktop.MenuItem{ID: "file.recent.none", Label: "(none)", Disabled: true})
	}
	return desktop.Menu{Items: []desktop.MenuItem{
		{ID: "file", Label: "&File", Submenu: &desktop.Menu{Items: []desktop.MenuItem{
			{ID: "file.new", Label: "&New Project...", OnClick: h.newProjectDialog},
			{ID: "file.open", Label: "&Open...", OnClick: h.openDialog},
			{ID: "file.saveas", Label: "Save &As...", OnClick: h.saveAsDialog},
			{Separator: true},
			{ID: "file.recent", Label: "Open &Recent", Submenu: &desktop.Menu{Items: recentItems}},
			{Separator: true},
			{ID: "file.exit", Label: "E&xit", OnClick: func() { _ = h.app.Close() }},
		}}},
		{ID: "view", Label: "&View", Submenu: &desktop.Menu{Items: []desktop.MenuItem{
			{ID: "view.reload", Label: "&Reload Studio", OnClick: func() { go h.open(h.currentScore()) }},
		}}},
		{ID: "help", Label: "&Help", Submenu: &desktop.Menu{Items: []desktop.MenuItem{
			{ID: "help.docs", Label: "Cicada &Documentation", OnClick: func() { _ = h.app.OpenURL("https://github.com/odvcencio/cicada#readme") }},
		}}},
	}}
}

var scoreFilter = []desktop.FileFilter{{Name: "Cicada score (*.cicada)", Pattern: "*.cicada"}}

func (h *host) openDialog() {
	paths, err := h.app.OpenFileDialog(desktop.OpenFileOptions{
		Title: "Open Cicada score", Filters: scoreFilter, DefaultExt: "cicada",
		InitialDir: filepath.Dir(h.currentScore()),
	})
	if err != nil || len(paths) == 0 {
		return
	}
	h.rememberAndOpen(paths[0])
}

func (h *host) saveAsDialog() {
	current := h.currentScore()
	if current == "" {
		return
	}
	target, err := h.app.SaveFileDialog(desktop.SaveFileOptions{
		Title: "Save Cicada score as", Filters: scoreFilter, DefaultExt: "cicada",
		InitialDir: filepath.Dir(current), InitialFilename: filepath.Base(current), OverwritePrompt: true,
	})
	if err != nil || target == "" {
		return
	}
	if err := saveCopyWithAssets(h.cicada, current, target); err != nil {
		showError("Save As failed", err.Error())
		return
	}
	h.rememberAndOpen(target)
}

func (h *host) newProjectDialog() {
	parent := filepath.Join(documentsDir(), "Cicada")
	_ = os.MkdirAll(parent, 0o755)
	target, err := h.app.SaveFileDialog(desktop.SaveFileOptions{
		Title: "New Cicada project (choose a folder name)", InitialDir: parent,
		InitialFilename: nextProjectName(parent), OverwritePrompt: false,
	})
	if err != nil || target == "" {
		return
	}
	name := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	score, err := newProject(h.cicada, filepath.Dir(target), name)
	if err != nil {
		showError("New project failed", err.Error())
		return
	}
	h.rememberAndOpen(score)
}

// rememberAndOpen runs on the window thread (menu handlers): it updates the
// recent list and menu at once, then switches in the background.
func (h *host) rememberAndOpen(score string) {
	if abs, err := filepath.Abs(score); err == nil {
		score = abs
	}
	h.mu.Lock()
	h.state.remember(score)
	h.mu.Unlock()
	_ = h.app.SetMenuBar(h.menu())
	go h.open(score)
}

func (h *host) onSecondInstance(message desktop.InstanceMessage) {
	for _, arg := range message.Args {
		if !strings.EqualFold(filepath.Ext(arg), ".cicada") {
			continue
		}
		if !filepath.IsAbs(arg) && message.WorkingDir != "" {
			arg = filepath.Join(message.WorkingDir, arg)
		}
		h.rememberAndOpen(arg)
		return
	}
}

func (h *host) onNavigationCompleted(event desktop.NavigationCompleted) {
	if h.smokeOut == "" {
		return
	}
	h.mu.Lock()
	proc := h.proc
	h.mu.Unlock()
	if proc == nil || !event.Success {
		return
	}
	timeline := h.app.StartupTimeline()
	h.recordSmoke("studioLoadedMs", time.Since(h.started).Milliseconds())
	h.recordSmoke("timelineMs", map[string]int64{
		"windowShown": timeline.WindowShown.Milliseconds(), "environmentReady": timeline.EnvironmentReady.Milliseconds(),
		"controllerReady": timeline.ControllerReady.Milliseconds(), "firstNavigationCompleted": timeline.FirstNavigationCompleted.Milliseconds(),
	})
	h.recordSmoke("score", h.currentScore())
	h.recordSmoke("sidecarPID", proc.PID())
	go func() {
		time.Sleep(h.smokeHold)
		_ = h.app.Close()
	}()
}

func (h *host) recordSmoke(key string, value any) {
	if h.smokeOut == "" {
		return
	}
	h.mu.Lock()
	h.smoke[key] = value
	h.mu.Unlock()
}

func (h *host) appendSmoke(key, value string) {
	if h.smokeOut == "" {
		return
	}
	h.mu.Lock()
	list, _ := h.smoke[key].([]string)
	h.smoke[key] = append(list, value)
	h.mu.Unlock()
}

func (h *host) writeSmoke() {
	h.mu.Lock()
	data, _ := json.MarshalIndent(h.smoke, "", " ")
	h.mu.Unlock()
	_ = os.WriteFile(h.smokeOut, data, 0o644)
}

func openLog(path string) (*os.File, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func documentsDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Documents")
	}
	return "."
}

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func statusPage(title, detail string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;height:100vh;display:grid;place-items:center;background:` + backgroundColor +
		`;color:#e2e9dc;font:16px/1.5 "Segoe UI",system-ui,sans-serif}p{color:#a6b5a6}</style></head><body><main><h1>` +
		html.EscapeString(title) + `</h1><p>` + html.EscapeString(detail) + `</p></main></body></html>`
}

func errorPage(score string, err error) string {
	return statusPage("Cicada Studio stopped", score+": "+err.Error()+". Use View > Reload Studio to try again; details are in the studio log.")
}

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

func showError(title, message string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(message)
	const mbIconError, mbSetForeground = 0x10, 0x10000
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError|mbSetForeground)
}
