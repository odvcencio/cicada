//go:build browser || browser_soak

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var browserStudioAddress = "127.0.0.1:8166"

var browserDebugAddress = "127.0.0.1:8165"

var browserEvidenceDir = func() string {
	if dir := os.Getenv("CICADA_BROWSER_EVIDENCE_DIR"); dir != "" {
		return dir
	}
	return filepath.Join("build", "browser-evidence")
}()

const windowsBrowserProfile = `C:\Temp\cicada-ws-m2c3`
const windowsBrowserProfileWSL = `/mnt/c/Temp/cicada-ws-m2c3`

func windowsStudioBaseURL() string {
	return "http://localhost:" + strings.TrimPrefix(browserStudioAddress, "127.0.0.1:")
}

type browserBridgeCommand struct {
	ID         string `json:"id"`
	Expression string `json:"expression"`
}

type browserBridgeResult struct {
	ID    string          `json:"id"`
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
}

type browserBridge struct {
	commands chan browserBridgeCommand
	mu       sync.Mutex
	waiting  map[string]chan browserBridgeResult
	nextID   uint64
	ready    chan struct{}
	readyOne sync.Once
}

func newBrowserBridge() *browserBridge {
	return &browserBridge{commands: make(chan browserBridgeCommand), waiting: make(map[string]chan browserBridgeResult), ready: make(chan struct{})}
}

func (b *browserBridge) markReady() { b.readyOne.Do(func() { close(b.ready) }) }

func (b *browserBridge) evaluate(expression string) (json.RawMessage, error) {
	b.mu.Lock()
	b.nextID++
	id := fmt.Sprintf("%d", b.nextID)
	result := make(chan browserBridgeResult, 1)
	b.waiting[id] = result
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
	}()
	command := browserBridgeCommand{ID: id, Expression: expression}
	select {
	case b.commands <- command:
	case <-time.After(20 * time.Second):
		return nil, fmt.Errorf("Windows Chrome page did not request a browser command")
	}
	select {
	case reply := <-result:
		if reply.Error != "" {
			return nil, fmt.Errorf("Windows Chrome evaluation: %s", reply.Error)
		}
		if len(reply.Value) == 0 {
			return json.RawMessage("null"), nil
		}
		return reply.Value, nil
	case <-time.After(3 * time.Minute):
		return nil, fmt.Errorf("Windows Chrome did not return a browser command result")
	}
}

func (b *browserBridge) handleNext(w http.ResponseWriter, r *http.Request) {
	b.markReady()
	w.Header().Set("Cache-Control", "no-store")
	select {
	case command := <-b.commands:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(command)
	case <-r.Context().Done():
	case <-time.After(20 * time.Second):
		w.WriteHeader(http.StatusNoContent)
	}
}

func (b *browserBridge) handleResult(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var result browserBridgeResult
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&result); err != nil {
		http.Error(w, "invalid browser command result", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	waiting := b.waiting[result.ID]
	b.mu.Unlock()
	if waiting != nil {
		select {
		case waiting <- result:
		default:
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

const windowsBrowserBridgeJS = `(() => {
  const client = Math.random().toString(36).slice(2);
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
  async function poll() {
    for (;;) {
      try {
        const response = await fetch('/__test/browser-bridge/next?client=' + client, {cache:'no-store'});
        if (response.status === 204) continue;
        if (!response.ok) throw new Error('browser command poll returned ' + response.status);
        const command = await response.json();
        let result;
        try {
          const value = await (0, eval)(command.expression);
          result = {id:command.id, value:value === undefined ? null : value};
        } catch (error) {
          result = {id:command.id, error:String(error && error.stack || error)};
        }
        await fetch('/__test/browser-bridge/result', {
          method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(result)
        });
      } catch (_) {
        await pause(250);
      }
    }
  }
  poll();
})();`

type browserStudioServer struct {
	server *http.Server
	score  string
	bridge *browserBridge
}

func startBrowserStudio(t *testing.T, source, reference []byte) *browserStudioServer {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "first-acid.cicada")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	base := s.routes()
	bridge := newBrowserBridge()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("CICADA_BROWSER") == "windows" {
			switch r.URL.Path {
			case "/__test/browser-bridge.js":
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write([]byte(windowsBrowserBridgeJS))
				return
			case "/__test/browser-bridge/next":
				bridge.handleNext(w, r)
				return
			case "/__test/browser-bridge/result":
				bridge.handleResult(w, r)
				return
			}
		}
		if r.URL.Path == "/__test/reference" && reference != nil {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(reference)
			return
		}
		if os.Getenv("CICADA_BROWSER") == "windows" && r.Method == http.MethodGet && r.URL.Path == "/" {
			capture := httptest.NewRecorder()
			base.ServeHTTP(capture, r)
			body := capture.Body.Bytes()
			if strings.Contains(capture.Header().Get("Content-Type"), "text/html") {
				body = bytes.Replace(body, []byte("</body>"), []byte(`<script src="/__test/browser-bridge.js"></script></body>`), 1)
				for key, values := range capture.Header() {
					if strings.EqualFold(key, "Content-Length") {
						continue
					}
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(capture.Code)
				_, _ = w.Write(body)
				return
			}
			for key, values := range capture.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(body)
			return
		}
		base.ServeHTTP(w, r)
	})
	addresses := []string{browserStudioAddress}
	if os.Getenv("CICADA_BROWSER") == "windows" {
		addresses = []string{"127.0.0.1:8165"}
	}
	var listener net.Listener
	var listenErr error
	for _, address := range addresses {
		listener, listenErr = net.Listen("tcp", address)
		if listenErr == nil {
			browserStudioAddress = address
			break
		}
	}
	if listenErr != nil {
		t.Fatalf("listen on available Studio port in %v: %v", addresses, listenErr)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	result := &browserStudioServer{server: server, score: path, bridge: bridge}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
		s.transport.close()
	})
	return result
}

type cdpTarget struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	WS   string `json:"webSocketDebuggerUrl"`
}

type cdpReply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type browserChrome struct {
	t        *testing.T
	process  *exec.Cmd
	done     chan struct{}
	conn     *websocket.Conn
	nextID   int
	log      *os.File
	external bool
	windows  bool
	bridge   *browserBridge
	outerW   int
	outerH   int
}

func startBrowserChrome(t *testing.T, server *browserStudioServer) *browserChrome {
	t.Helper()
	if os.Getenv("CICADA_BROWSER") == "windows" {
		return startWindowsBrowserChrome(t, server)
	}
	bin := os.Getenv("CHROME_BIN")
	if bin == "" {
		for _, candidate := range []string{"google-chrome", "chrome-headless-shell", "chromium"} {
			if path, err := exec.LookPath(candidate); err == nil {
				bin = path
				break
			}
		}
	}
	if bin == "" {
		t.Fatal("Chrome is required; set CHROME_BIN to Chrome for Testing headless shell")
	}
	version := exec.Command(bin, "--mute-audio", "--version")
	version.Env = mutedAudioEnv()
	if output, err := version.CombinedOutput(); err == nil {
		t.Logf("headless Chrome: %s", strings.TrimSpace(string(output)))
	}
	logPath := filepath.Join("build", "m2-browser-chrome.log")
	external := os.Getenv("CICADA_BROWSER_CHROME_EXTERNAL") == "1"
	chrome := &browserChrome{t: t, external: external}
	if !external {
		if err := os.MkdirAll("build", 0755); err != nil {
			t.Fatal(err)
		}
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		profile := filepath.Join(t.TempDir(), "profile")
		process := exec.Command(bin,
			"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
			"--no-first-run", "--no-default-browser-check", "--mute-audio",
			"--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+strings.TrimPrefix(browserDebugAddress, "127.0.0.1:"),
			"--user-data-dir="+profile, "about:blank",
		)
		process.Env = mutedAudioEnv()
		process.Stdout, process.Stderr = logFile, logFile
		if err := process.Start(); err != nil {
			_ = logFile.Close()
			t.Fatal(err)
		}
		chrome.process, chrome.log, chrome.done = process, logFile, make(chan struct{})
		go func() { _ = process.Wait(); close(chrome.done) }()
	}
	t.Cleanup(chrome.close)
	deadline := time.Now().Add(20 * time.Second)
	var target cdpTarget
	for time.Now().Before(deadline) {
		response, requestErr := http.Get("http://" + browserDebugAddress + "/json/list")
		if requestErr == nil {
			var targets []cdpTarget
			_ = json.NewDecoder(response.Body).Decode(&targets)
			_ = response.Body.Close()
			for _, candidate := range targets {
				if candidate.Type == "page" && candidate.WS != "" {
					target = candidate
					break
				}
			}
			if target.WS != "" {
				break
			}
		}
		if chrome.done != nil {
			select {
			case <-time.After(100 * time.Millisecond):
			case <-chrome.done:
				t.Fatalf("Chrome exited during startup; see %s", logPath)
			}
		} else {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if target.WS == "" {
		t.Fatalf("Chrome DevTools did not start on %s; see %s", browserDebugAddress, logPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, target.WS, nil)
	if err != nil {
		t.Fatalf("connect to Chrome DevTools: %v", err)
	}
	chrome.conn = connection
	chrome.conn.SetReadLimit(16 << 20)
	chrome.mustCall("Page.enable", nil)
	chrome.mustCall("Runtime.enable", nil)
	return chrome
}

func startWindowsBrowserChrome(t *testing.T, server *browserStudioServer) *browserChrome {
	t.Helper()
	if server == nil || server.bridge == nil {
		t.Fatal("Windows Chrome requires the WSL page-result collector")
	}
	bin := "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe"
	if _, err := os.Stat(bin); err != nil {
		bin = "/mnt/c/Program Files (x86)/Google/Chrome/Application/chrome.exe"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("Windows Chrome was not found at the standard install paths: %v", err)
	}
	powershell, err := windowsPowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(windowsBrowserProfileWSL, 0700); err != nil {
		t.Fatalf("create Windows Chrome profile directory: %v", err)
	}
	stopWindowsChrome(t, powershell)
	if err := os.MkdirAll("build", 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join("build", "m2-browser-chrome.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	url := windowsStudioBaseURL() + "/"
	process := exec.Command(bin,
		"--app="+url,
		"--user-data-dir="+windowsBrowserProfile,
		"--no-first-run", "--no-default-browser-check", "--disable-background-mode",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding",
		"--disable-backgrounding-occluded-windows", "--disable-features=CalculateNativeWinOcclusion,IntensiveWakeUpThrottling",
		"--autoplay-policy=no-user-gesture-required", "--force-device-scale-factor=1",
		"--window-position=30,30", "--window-size=1440,1000", "--mute-audio",
	)
	process.Env = mutedAudioEnv()
	process.Stdout, process.Stderr = logFile, logFile
	if err := process.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start chrome.exe through WSL interop: %v", err)
	}
	chrome := &browserChrome{t: t, process: process, done: make(chan struct{}), log: logFile, windows: true, bridge: server.bridge, outerW: 1440, outerH: 1000}
	go func() { _ = process.Wait(); close(chrome.done) }()
	t.Cleanup(chrome.close)
	select {
	case <-server.bridge.ready:
	case <-time.After(30 * time.Second):
		chrome.close()
		t.Fatalf("Windows Chrome did not POST a result-channel poll to %s; see %s", url, logPath)
	}
	return chrome
}

func windowsPowerShellPath() (string, error) {
	path := "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("Windows PowerShell interop was not found at %s: %w", path, err)
	}
	return path, nil
}

type windowsChromeCPUReading struct {
	Processes      int                       `json:"processes"`
	CapturedUnixMs int64                     `json:"capturedUnixMs"`
	ProcessCPU     []windowsChromeProcessCPU `json:"processCPU"`
}

type windowsChromeProcessCPU struct {
	ProcessID    int    `json:"processId"`
	CPUTime100ns uint64 `json:"cpuTime100ns"`
}

func windowsChromeRendererCPUMsPerCallback(before, after windowsChromeCPUReading, quantumMs float64) (float64, float64, int, float64, bool) {
	intervalMs := float64(after.CapturedUnixMs - before.CapturedUnixMs)
	if intervalMs <= 0 || quantumMs <= 0 || len(before.ProcessCPU) != before.Processes || len(after.ProcessCPU) != after.Processes || before.Processes != after.Processes {
		return 0, 0, 0, intervalMs, false
	}
	prior := make(map[int]uint64, len(before.ProcessCPU))
	for _, process := range before.ProcessCPU {
		prior[process.ProcessID] = process.CPUTime100ns
	}
	var maxTicks, totalTicks uint64
	var maxPID int
	matched := 0
	for _, process := range after.ProcessCPU {
		start, ok := prior[process.ProcessID]
		if !ok {
			return 0, 0, 0, intervalMs, false
		}
		matched++
		if process.CPUTime100ns < start {
			return 0, 0, 0, intervalMs, false
		}
		delta := process.CPUTime100ns - start
		totalTicks += delta
		if delta > maxTicks {
			maxTicks, maxPID = delta, process.ProcessID
		}
	}
	if matched != len(prior) {
		return 0, 0, 0, intervalMs, false
	}
	return float64(maxTicks) / 10_000 * quantumMs / intervalMs, float64(totalTicks) / 10_000 * quantumMs / intervalMs, maxPID, intervalMs, true
}

func windowsChromeRendererCPU(t *testing.T) windowsChromeCPUReading {
	t.Helper()
	powershell, err := windowsPowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	script := `$ErrorActionPreference='Stop'; $laneUserData='` + windowsBrowserProfile + `'; $processes=@(Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like ('*' + $laneUserData + '*') -and $_.CommandLine -match '--type=renderer(?:\s|$)' }); if ($processes.Count -eq 0) { throw 'lane Chrome renderer process not found' }; $processCPU=@($processes | ForEach-Object { [ordered]@{processId=[int]$_.ProcessId;cpuTime100ns=([long]$_.UserModeTime + [long]$_.KernelModeTime)} }); $capturedUnixMs=[DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds(); [ordered]@{processes=$processCPU.Count;capturedUnixMs=$capturedUnixMs;processCPU=$processCPU} | ConvertTo-Json -Compress`
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("read Windows Chrome renderer CPU counters: %v: %s", err, strings.TrimSpace(string(output)))
	}
	var reading windowsChromeCPUReading
	if err := json.Unmarshal(output, &reading); err != nil {
		t.Fatalf("decode Windows Chrome renderer CPU counters %q: %v", strings.TrimSpace(string(output)), err)
	}
	if reading.Processes == 0 || len(reading.ProcessCPU) != reading.Processes {
		t.Fatal("Windows Chrome renderer CPU counter did not include the lane profile")
	}
	return reading
}

func stopWindowsChrome(t *testing.T, powershell string) {
	t.Helper()
	script := `$laneUserData='` + windowsBrowserProfile + `'; Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like ('*' + $laneUserData + '*') } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("close only Windows Chrome processes for %s: %v: %s", windowsBrowserProfile, err, strings.TrimSpace(string(output)))
	}
}

func mutedAudioEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PULSE_SERVER=") {
			env = append(env, entry)
		}
	}
	return append(env, "PULSE_SERVER=unix:/nonexistent")
}

func (b *browserChrome) close() {
	if b.windows {
		if powershell, err := windowsPowerShellPath(); err == nil {
			stopWindowsChrome(b.t, powershell)
		}
	}
	if b.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		b.nextID++
		method, params := "Browser.close", map[string]any{}
		if b.external {
			method, params = "Page.navigate", map[string]any{"url": "about:blank"}
		}
		_ = wsjson.Write(ctx, b.conn, map[string]any{"id": b.nextID, "method": method, "params": params})
		cancel()
		_ = b.conn.Close(websocket.StatusNormalClosure, "test finished")
	}
	if b.process != nil && b.process.Process != nil {
		select {
		case <-b.done:
		case <-time.After(5 * time.Second):
			_ = b.process.Process.Kill()
			<-b.done
		}
	}
	if b.log != nil {
		_ = b.log.Close()
	}
}

func (b *browserChrome) call(method string, params any) json.RawMessage {
	b.t.Helper()
	if b.windows {
		switch method {
		case "Page.reload":
			b.eval(`setTimeout(()=>location.reload(),250);true`)
			return json.RawMessage(`{}`)
		default:
			b.t.Fatalf("Windows Chrome mode does not expose DevTools method %s", method)
		}
	}
	b.nextID++
	id := b.nextID
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := wsjson.Write(ctx, b.conn, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		b.t.Fatalf("CDP %s write: %v", method, err)
	}
	for {
		var reply cdpReply
		if err := wsjson.Read(ctx, b.conn, &reply); err != nil {
			b.t.Fatalf("CDP %s read: %v", method, err)
		}
		if reply.ID != id {
			continue
		}
		if reply.Error != nil {
			b.t.Fatalf("CDP %s: %s", method, reply.Error.Message)
		}
		return reply.Result
	}
}

func (b *browserChrome) mustCall(method string, params any) json.RawMessage {
	b.t.Helper()
	return b.call(method, params)
}

func (b *browserChrome) navigate(url string) {
	b.t.Helper()
	if b.windows {
		if strings.TrimSuffix(strings.Replace(url, "127.0.0.1", "localhost", 1), "/") != windowsStudioBaseURL() {
			encoded, _ := json.Marshal(url)
			b.eval(`setTimeout(()=>location.href=` + string(encoded) + `,250);true`)
		}
		b.waitFor("document.readyState === 'complete'", 20*time.Second)
		return
	}
	b.mustCall("Page.navigate", map[string]any{"url": url})
	b.waitFor("document.readyState === 'complete'", 20*time.Second)
}

func (b *browserChrome) setViewport(width, height int) {
	b.setViewportMode(width, height, width <= 600)
}

func (b *browserChrome) setViewportMode(width, height int, mobile bool) {
	b.t.Helper()
	if b.windows {
		b.setWindowsViewport(width, height)
		return
	}
	b.mustCall("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": mobile,
	})
}

func (b *browserChrome) eval(expression string) json.RawMessage {
	b.t.Helper()
	if b.windows {
		value, err := b.bridge.evaluate(expression)
		if err != nil {
			b.t.Fatalf("Windows Chrome evaluation: %v", err)
		}
		return value
	}
	result := b.call("Runtime.evaluate", map[string]any{
		"expression": expression, "awaitPromise": true, "returnByValue": true,
	})
	var evaluated struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evaluated); err != nil {
		b.t.Fatalf("decode CDP evaluation: %v", err)
	}
	if evaluated.Exception != nil {
		b.t.Fatalf("browser evaluation: %s: %s", evaluated.Exception.Text, evaluated.Exception.Exception.Description)
	}
	return evaluated.Result.Value
}

func (b *browserChrome) waitFor(expression string, timeout time.Duration) {
	b.t.Helper()
	deadline := time.Now().Add(timeout)
	var last json.RawMessage
	for time.Now().Before(deadline) {
		last = b.eval(expression)
		var ok bool
		if json.Unmarshal(last, &ok) == nil && ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b.t.Fatalf("browser condition timed out: %s; last value %s", expression, last)
}

func (b *browserChrome) click(selector string) {
	b.t.Helper()
	if b.windows {
		encoded := strconvQuote(selector)
		result := b.eval(`(()=>{const element=document.querySelector(` + encoded + `);if(!element)return false;element.click();return true})()`)
		if string(result) != "true" {
			b.t.Fatalf("cannot click %s; browser returned %s", selector, result)
		}
		return
	}
	var point struct{ X, Y float64 }
	encoded := b.eval(`(()=>{const e=document.querySelector(` + strconvQuote(selector) + `);if(!e)return null;const r=e.getBoundingClientRect();return{x:r.left+r.width/2,y:r.top+r.height/2}})()`)
	if err := json.Unmarshal(encoded, &point); err != nil || point.X <= 0 || point.Y <= 0 {
		b.t.Fatalf("cannot click %s at %s", selector, encoded)
	}
	for _, event := range []string{"mousePressed", "mouseReleased"} {
		b.mustCall("Input.dispatchMouseEvent", map[string]any{"type": event, "x": point.X, "y": point.Y, "button": "left", "clickCount": 1})
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (b *browserChrome) screenshot(name string) {
	b.t.Helper()
	if b.windows {
		b.captureWindowsScreenshot(name)
		return
	}
	response := b.mustCall("Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true})
	var encoded struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(response, &encoded); err != nil {
		b.t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(encoded.Data)
	if err != nil {
		b.t.Fatal(err)
	}
	if err := os.MkdirAll(browserEvidenceDir, 0755); err != nil {
		b.t.Fatal(err)
	}
	path := filepath.Join(browserEvidenceDir, name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		b.t.Fatal(err)
	}
	b.t.Logf("screenshot: %s", path)
}

func (b *browserChrome) setWindowsViewport(width, height int) {
	b.t.Helper()
	powershell, err := windowsPowerShellPath()
	if err != nil {
		b.t.Fatal(err)
	}
	type size struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	for attempt := 0; attempt < 5; attempt++ {
		var current size
		if err := json.Unmarshal(b.eval(`({width:window.innerWidth,height:window.innerHeight})`), &current); err != nil {
			b.t.Fatalf("read Windows Chrome viewport: %v", err)
		}
		if current.Width == width && current.Height == height {
			return
		}
		b.outerW += width - current.Width
		b.outerH += height - current.Height
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'; Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class CicadaWindow { [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int cx, int cy, uint flags); [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd); }'; $laneUserData='%s'; $window=$null; Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like ('*' + $laneUserData + '*') } | ForEach-Object { $p=Get-Process -Id $_.ProcessId -ErrorAction SilentlyContinue; if ($p -and $p.MainWindowHandle -ne 0) { $window=$p.MainWindowHandle } }; if (-not $window) { throw 'lane Chrome window not found' }; [CicadaWindow]::SetWindowPos([IntPtr]$window,[IntPtr]::Zero,30,30,%d,%d,0x0040) | Out-Null; [CicadaWindow]::SetForegroundWindow([IntPtr]$window) | Out-Null`, windowsBrowserProfile, b.outerW, b.outerH)
		command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
		if output, err := command.CombinedOutput(); err != nil {
			b.t.Fatalf("resize Windows Chrome window: %v: %s", err, strings.TrimSpace(string(output)))
		}
		time.Sleep(250 * time.Millisecond)
	}
	actual := b.eval(`({width:window.innerWidth,height:window.innerHeight})`)
	b.t.Fatalf("Windows Chrome viewport did not reach %dx%d: %s", width, height, actual)
}

func (b *browserChrome) captureWindowsScreenshot(name string) {
	b.t.Helper()
	powershell, err := windowsPowerShellPath()
	if err != nil {
		b.t.Fatal(err)
	}
	if err := os.MkdirAll(windowsBrowserProfileWSL, 0700); err != nil {
		b.t.Fatal(err)
	}
	windowsPath := windowsBrowserProfile + `\` + strings.ReplaceAll(filepath.Base(name), "'", "''")
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.Drawing; Add-Type -TypeDefinition 'using System; using System.Runtime.InteropServices; public static class CicadaCapture { [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left; public int Top; public int Right; public int Bottom; } [StructLayout(LayoutKind.Sequential)] public struct Point { public int X; public int Y; } [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr hWnd, out Rect rect); [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr hWnd, ref Point point); [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd); }'; $laneUserData='%s'; $window=$null; Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'chrome.exe' -and $_.CommandLine -like ('*' + $laneUserData + '*') } | ForEach-Object { $p=Get-Process -Id $_.ProcessId -ErrorAction SilentlyContinue; if ($p -and $p.MainWindowHandle -ne 0) { $window=$p.MainWindowHandle } }; if (-not $window) { throw 'lane Chrome window not found' }; [CicadaCapture]::SetForegroundWindow([IntPtr]$window) | Out-Null; $rect=New-Object CicadaCapture+Rect; if (-not [CicadaCapture]::GetClientRect([IntPtr]$window,[ref]$rect)) { throw 'GetClientRect failed' }; $point=New-Object CicadaCapture+Point; if (-not [CicadaCapture]::ClientToScreen([IntPtr]$window,[ref]$point)) { throw 'ClientToScreen failed' }; $bitmap=New-Object System.Drawing.Bitmap($rect.Right,$rect.Bottom); $graphics=[System.Drawing.Graphics]::FromImage($bitmap); $graphics.CopyFromScreen($point.X,$point.Y,0,0,$bitmap.Size); $bitmap.Save('%s',[System.Drawing.Imaging.ImageFormat]::Png); $graphics.Dispose(); $bitmap.Dispose()`, windowsBrowserProfile, windowsPath)
	command := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	if output, err := command.CombinedOutput(); err != nil {
		b.t.Fatalf("capture headed Windows Chrome screenshot: %v: %s", err, strings.TrimSpace(string(output)))
	}
	source := filepath.Join(windowsBrowserProfileWSL, filepath.Base(name))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(source); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		b.t.Fatalf("read Windows Chrome screenshot %s: %v", source, err)
	}
	if err := os.MkdirAll(browserEvidenceDir, 0755); err != nil {
		b.t.Fatal(err)
	}
	destination := filepath.Join(browserEvidenceDir, filepath.Base(name))
	if err := os.WriteFile(destination, data, 0644); err != nil {
		b.t.Fatal(err)
	}
	b.t.Logf("headed Windows Chrome screenshot: %s", destination)
}

func nativeReference(t *testing.T, source []byte, bars int, rate int) []byte {
	t.Helper()
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse audio reference: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project audio reference: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("native reference rejected play")
	}
	clock, err := seq.NewClock(rate, int64(p.TempoMilli))
	if err != nil {
		t.Fatal(err)
	}
	end := clock.SampleAtTick(int64(bars) * seq.TicksPerBar)
	blocks := int((end + 127) / 128)
	frames := blocks * 128
	data := make([]byte, 8+frames*8)
	binary.LittleEndian.PutUint32(data[:4], uint32(frames))
	left, right := make([]float32, 128), make([]float32, 128)
	messageLog := make([]byte, 0, 8<<10)
	for block := 0; block < blocks; block++ {
		e.Render(left, right)
		for frame := 0; frame < 128; frame++ {
			at := 8 + (block*128+frame)*8
			binary.LittleEndian.PutUint32(data[at:], math.Float32bits(left[frame]))
			binary.LittleEndian.PutUint32(data[at+4:], math.Float32bits(right[frame]))
		}
		if block&7 == 7 {
			var message cmd.Message
			for e.Poll(&message) {
				record := cmd.EncodeMessage(message)
				messageLog = append(messageLog, record[:]...)
			}
		}
	}
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(messageLog)))
	return append(data, messageLog...)
}

func browserLogError(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprint(err)
	}
	return string(data)
}
