//go:build js && wasm

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

// Route changes are blocked during playback/capture. Stop does not rebuild
// the workspace, so release those controls from the native state directly.
func (u *workspaceUI) watchAudioControls() {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	read := func(path string, target any) bool {
		request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+path, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200 && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target) == nil
	}
	for {
		if u.closed.Load() {
			return
		}
		if node := u.root.Call("querySelector", "[data-audio-routing]"); workspacePresent(node) {
			var state struct {
				Playing bool                 `json:"playing"`
				Runtime control.AudioRuntime `json:"runtime"`
				Options struct {
					PeriodFrames int `json:"periodFrames"`
				} `json:"options"`
			}
			capture := nativeCapture.Get()
			if read("/api/audio/config?runtime=1", &state) && !u.closed.Load() && node.Get("isConnected").Bool() {
				locked := state.Playing || state.Runtime.Armed || capture.Ready && !capture.Error && capture.State.Active != ""
				node.Set("disabled", locked)
				if notice := u.root.Call("querySelector", "[data-audio-lock-notice]"); workspacePresent(notice) {
					notice.Set("hidden", !locked)
				}
				for _, row := range control.AudioRuntimeRows(state.Runtime, state.Options.PeriodFrames) {
					if value := u.root.Call("querySelector", "[data-audio-value="+row.Key+"]"); workspacePresent(value) && value.Get("textContent").String() != row.Value {
						value.Set("textContent", row.Value)
					}
				}
				if warning := u.root.Call("querySelector", "[data-audio-runtime-warning]"); workspacePresent(warning) {
					message := control.AudioRuntimeWarning(state.Runtime)
					if warning.Get("textContent").String() != message {
						warning.Set("textContent", message)
					}
					warning.Set("hidden", message == "")
				}
			}
		}
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
