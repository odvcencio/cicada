//go:build browser

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBrowserContinuousAutomationSession(t *testing.T) {
	bin := os.Getenv("CHROME_BIN")
	if bin == "" {
		t.Fatal("set CHROME_BIN to a headless Chrome executable")
	}
	app := continuousAutomationApp(t)
	evidence := os.Getenv("CICADA_BROWSER_EVIDENCE_DIR")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	log, err := os.Create(filepath.Join(t.TempDir(), "chrome.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bin, "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage", "--disable-background-networking", "--mute-audio", "--no-first-run", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		finished := make(chan error, 1)
		go func() { finished <- command.Wait() }()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			cancel()
			<-finished
		}
	}()
	var port string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort"))
		if err == nil {
			port = strings.Split(string(data), "\n")[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if port == "" {
		t.Fatal("Chrome did not start its debugging endpoint")
	}
	response, err := http.Get("http://127.0.0.1:" + port + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	var targets []struct {
		Type                 string
		WebSocketDebuggerURL string
	}
	err = json.NewDecoder(response.Body).Decode(&targets)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var address string
	for _, target := range targets {
		if target.Type == "page" {
			address = target.WebSocketDebuggerURL
			break
		}
	}
	connection, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = connection.WriteJSON(map[string]any{"id": 10000, "method": "Browser.close"})
		_ = connection.Close()
	}()
	id := 0
	call := func(method string, params any) json.RawMessage {
		t.Helper()
		id++
		connection.SetReadDeadline(time.Now().Add(15 * time.Second))
		if err := connection.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		for {
			var result struct {
				ID     int
				Result json.RawMessage
				Error  json.RawMessage
			}
			if err := connection.ReadJSON(&result); err != nil {
				t.Fatal(err)
			}
			if result.ID != id {
				continue
			}
			if len(result.Error) != 0 {
				t.Fatalf("%s: %s", method, result.Error)
			}
			return result.Result
		}
	}
	evaluate := func(expression string) bool {
		t.Helper()
		data := call("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
		var result struct{ Result struct{ Value bool } }
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		return result.Result.Value
	}
	call("Page.enable", map[string]any{})
	for _, width := range []int{1440, 390} {
		call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": 1000, "deviceScaleFactor": 1, "mobile": width < 600})
		call("Page.navigate", map[string]any{"url": app.URL + "/?panel=session"})
		ready := false
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if evaluate(`document.querySelectorAll('#continuous-automation .continuous-curve').length === 2`) {
				ready = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			t.Fatal("continuous automation curves did not render")
		}
		if !evaluate(`document.documentElement.scrollWidth <= innerWidth`) {
			t.Fatal("session page overflows viewport")
		}
		if !evaluate(`Array.from(document.querySelectorAll('.continuous-curve')).every(p=>p.getAttribute('d').startsWith('M ') && !/NaN|Inf/.test(p.getAttribute('d')))`) {
			t.Fatal("invalid automation curve")
		}
		call("Runtime.evaluate", map[string]any{"expression": `document.getElementById('continuous-automation').scrollIntoView()`})
		data := call("Page.captureScreenshot", map[string]any{"format": "png", "captureBeyondViewport": false})
		var screenshot struct{ Data string }
		if err := json.Unmarshal(data, &screenshot); err != nil {
			t.Fatal(err)
		}
		pixels, err := base64.StdEncoding.DecodeString(screenshot.Data)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(evidence, fmt.Sprintf("continuous-automation-gosx-%d.png", width))
		if err := os.WriteFile(path, pixels, 0600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		config, err := png.DecodeConfig(file)
		file.Close()
		if err != nil || config.Width != width || config.Height != 1000 {
			t.Fatalf("screenshot dimensions: %+v, %v", config, err)
		}
		t.Logf("screenshot: %s", path)
	}
}
