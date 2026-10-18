//go:build windows || darwin

package main

// The desktop host shared by Windows and macOS. Everything that calls the
// operating system directly lives in host_windows.go and host_darwin.go:
// defaultDataDir, webViewDataDir, hideConsole and showError.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"m31labs.dev/gosx/desktop"
	"m31labs.dev/gosx/desktop/sidecar"
)

const (
	appID           = "dev.cicada.studio"
	backgroundColor = "#0f1411"
)

var buildVersion = "dev"

type host struct {
	mu          sync.Mutex
	app         *desktop.App
	cicada      string
	audio       string
	logOut      io.Writer
	statePath   string
	state       hostState
	score       string
	proc        *sidecar.Process
	queue       openQueue
	closing     bool
	starting    sync.WaitGroup
	startCancel context.CancelFunc

	smokeOut    string
	smokeHold   time.Duration
	smokeScript string
	started     time.Time
	smoke       map[string]any
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
	cicadaPath := flags.String("cicada", "", "path to the cicada executable (default: next to this program, then PATH)")
	audio := flags.String("audio", "", "audio backend passed to cicada studio: tymbal, oto, or null")
	dataDir := flags.String("data-dir", "", "folder for settings and logs (and the WebView2 profile on Windows)")
	mute := flags.Bool("mute", false, "mute the Studio page (tests; no effect on macOS)")
	devTools := flags.Bool("devtools", false, "enable developer tools (F12 on Windows, the context menu on macOS)")
	smokeOut := flags.String("smoke-out", "", "write a startup report to this JSON file and exit once Studio loads (tests)")
	smokeHold := flags.Duration("smoke-hold", 1500*time.Millisecond, "with --smoke-out, how long to keep Studio open after it loads")
	smokeScript := flags.String("smoke-script", "", "with --smoke-out, run this JavaScript workflow and await its result (tests)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	var script []byte
	if *smokeScript != "" {
		if *smokeOut == "" {
			return fmt.Errorf("--smoke-script requires --smoke-out")
		}
		var err error
		script, err = os.ReadFile(*smokeScript)
		if err != nil {
			return fmt.Errorf("read smoke script: %w", err)
		}
		if len(script) == 0 {
			return fmt.Errorf("smoke script is empty")
		}
	}
	if *dataDir == "" {
		dir, err := defaultDataDir()
		if err != nil {
			return err
		}
		*dataDir = dir
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
		cicada:      cicada,
		audio:       *audio,
		logOut:      logFile,
		statePath:   filepath.Join(*dataDir, "state.json"),
		smokeOut:    *smokeOut,
		smokeHold:   *smokeHold,
		smokeScript: string(script),
		started:     started,
		smoke:       map[string]any{},
	}
	h.state = loadState(h.statePath)
	score, err := resolveInitialScore(flags.Args(), h.state, cicada, filepath.Join(documentsDir(), "Cicada"))
	if err != nil {
		return err
	}

	h.app, err = desktop.New(desktop.Options{
		Title:            "Cicada Studio",
		Width:            1280,
		Height:           840,
		AppID:            appID,
		Version:          buildVersion,
		HTML:             statusPage("Starting Cicada Studio", filepath.Base(score)),
		BackgroundColor:  backgroundColor,
		UserDataDir:      webViewDataDir(*dataDir),
		MuteAudio:        *mute,
		DevTools:         *devTools,
		SingleInstance:   *smokeOut == "",
		OnSecondInstance: h.onSecondInstance,
		OnOpenFiles: func(paths []string) {
			h.onSecondInstance(desktop.InstanceMessage{Args: paths})
		},
		OnWebMessage: h.onSmokeMessage,
		OnNavigationCompleted: func(event desktop.NavigationCompleted) {
			h.onNavigationCompleted(event)
		},
		// Start the audio engine once this process owns the window, while
		// the web view starts. Starting it earlier would also start it in a
		// second launch that only forwards its file and exits.
		OnWindowCreated: func(*desktop.Window) { h.open(score) },
		OnClose:         h.shutdown,
	})
	if err != nil {
		return err
	}
	closeMIDI, err := attachNativeMIDI(h.app)
	if err != nil {
		return fmt.Errorf("configure native MIDI: %w", err)
	}
	defer closeMIDI()
	if err := h.optionalChrome("minimum window size", h.app.SetMinSize(800, 560)); err != nil {
		return err
	}
	if err := h.optionalChrome("menu bar", h.app.SetMenuBar(h.menu())); err != nil {
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
	// Catch SIGTERM/SIGINT so a shell kill (or launchd quitting the bundle)
	// closes the window through the UI backend, which runs OnClose and stops
	// the sidecar. Without this, the Go runtime exits immediately on SIGTERM
	// (exit 143) and the cicada + cicada-workstation children are orphaned.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	go func() {
		if _, ok := <-signals; ok {
			fmt.Fprintf(h.logOut, "%s host: signal received, closing\n", time.Now().Format(time.RFC3339))
			_ = h.app.Close()
		}
	}()
	runErr := h.app.Run()
	h.shutdown()
	if h.smokeOut != "" {
		h.writeSmoke()
	}
	return runErr
}

// optionalChrome passes through the result of a window or menu call that a
// platform backend may not provide yet. desktop.ErrUnsupported (the GoSX
// darwin backend before its menu slice) is logged and skipped so Studio still
// opens without that chrome; any other error stops startup.
func (h *host) optionalChrome(what string, err error) error {
	if errors.Is(err, desktop.ErrUnsupported) {
		fmt.Fprintf(h.logOut, "%s host: %s skipped: %v\n", time.Now().Format(time.RFC3339), what, err)
		return nil
	}
	return err
}

// open reserves requests on the calling thread, then switches in the background.
// Finder's first document must follow startup even if its goroutine runs first.
func (h *host) open(score string) {
	h.mu.Lock()
	closing := h.closing
	h.mu.Unlock()
	if closing || !h.queue.request(score) {
		return
	}
	go func() {
		for score != "" {
			h.openScore(score)
			score = h.queue.done()
		}
	}()
}

func (h *host) openScore(score string) {
	h.mu.Lock()
	closing, previous := h.closing, h.proc
	h.mu.Unlock()
	if closing {
		return
	}

	if previous != nil {
		_ = h.app.SetHTML(statusPage("Opening", filepath.Base(score)))
	}
	sidecarStart := time.Now()
	proc, err := h.startSidecar(score)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
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
		return
	}
	h.score = score
	h.state.remember(score)
	state := h.state
	h.mu.Unlock()
	if err := saveState(h.statePath, state); err != nil {
		fmt.Fprintf(h.logOut, "host: save state: %v\n", err)
	}
	_ = h.app.SetTitle(windowTitle(score))
	if err := h.app.Navigate(strings.TrimRight(proc.Ready(), "/") + "/studio"); err != nil {
		fmt.Fprintf(h.logOut, "host: navigate: %v\n", err)
	}
	go h.watch(proc, score)
}

// startSidecar keeps shutdown aware of a launch before readiness exposes its
// Process. It must not call the UI: OnClose waits for this work on the UI thread.
func (h *host) startSidecar(score string) (*sidecar.Process, error) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return nil, context.Canceled
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.startCancel = cancel
	h.starting.Add(1)
	previous := h.proc
	h.proc = nil
	h.mu.Unlock()
	defer h.starting.Done()
	defer cancel()
	if previous != nil {
		_ = previous.Stop(3 * time.Second)
	}
	fmt.Fprintf(h.logOut, "%s host: starting %s %s\n", time.Now().Format(time.RFC3339), h.cicada, strings.Join(studioArgs(score, h.audio), " "))
	proc, err := sidecar.Start(ctx, sidecar.Options{
		Path:         h.cicada,
		Args:         studioArgs(score, h.audio),
		Dir:          filepath.Dir(score),
		Output:       h.logOut,
		ReadyLine:    studioReadyLine,
		ReadyTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		_ = proc.Stop(time.Second)
		return nil, context.Canceled
	}
	h.proc = proc
	h.mu.Unlock()
	return proc, nil
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
	cancel := h.startCancel
	proc := h.proc
	h.proc = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proc != nil {
		_ = proc.Stop(2 * time.Second)
	}
	h.starting.Wait()
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
			OnClick: func() { h.open(file) },
		})
	}
	if len(recentItems) == 0 {
		recentItems = append(recentItems, desktop.MenuItem{ID: "file.recent.none", Label: "(none)", Disabled: true})
	}
	return platformMenu(h, desktop.Menu{Items: []desktop.MenuItem{
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
			{ID: "view.reload", Label: "&Reload Studio", OnClick: func() { h.open(h.currentScore()) }},
		}}},
		{ID: "help", Label: "&Help", Submenu: &desktop.Menu{Items: []desktop.MenuItem{
			{ID: "help.docs", Label: "Cicada &Documentation", OnClick: func() { _ = h.app.OpenURL("https://github.com/odvcencio/cicada#readme") }},
		}}},
	}})
}

var scoreFilter = []desktop.FileFilter{{Name: "Cicada score (*.cicada)", Pattern: "*.cicada"}}

func (h *host) openDialog() {
	paths, err := h.app.OpenFileDialog(desktop.OpenFileOptions{
		Title: "Open Cicada score", Filters: scoreFilter, DefaultExt: "cicada",
		InitialDir: filepath.Dir(h.currentScore()),
	})
	if err != nil {
		showError("Open failed", err.Error())
		return
	}
	if len(paths) == 0 {
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
	if err != nil {
		showError("Save As failed", err.Error())
		return
	}
	if target == "" {
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
	if err != nil {
		showError("New project failed", err.Error())
		return
	}
	if target == "" {
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
	h.open(score)
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
	if h.smokeScript != "" {
		if err := h.app.ExecuteScript(h.smokeScript); err != nil {
			h.recordSmoke("frontend", map[string]any{"ok": false, "error": err.Error()})
			go h.app.Close()
		}
		return
	}
	go func() {
		time.Sleep(h.smokeHold)
		_ = h.app.Close()
	}()
}

func (h *host) onSmokeMessage(message string) {
	if h.smokeOut == "" || h.smokeScript == "" {
		return
	}
	var result struct {
		Type   string   `json:"type"`
		OK     bool     `json:"ok"`
		Checks []string `json:"checks"`
		Error  string   `json:"error,omitempty"`
	}
	if json.Unmarshal([]byte(message), &result) != nil || result.Type != "cicada-native-smoke" {
		return
	}
	h.recordSmoke("frontend", result)
	go h.app.Close()
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

func statusPage(title, detail string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0;height:100vh;display:grid;place-items:center;background:` + backgroundColor +
		`;color:#e2e9dc;font:16px/1.5 "Segoe UI",system-ui,sans-serif}p{color:#a6b5a6}</style></head><body><main><h1>` +
		html.EscapeString(title) + `</h1><p>` + html.EscapeString(detail) + `</p></main></body></html>`
}

func errorPage(score string, err error) string {
	return statusPage("Cicada Studio stopped", score+": "+err.Error()+". Use View > Reload Studio to try again; details are in the studio log.")
}
