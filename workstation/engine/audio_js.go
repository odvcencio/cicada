//go:build js && wasm

package main

import (
	"context"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

// Both Studio layouts share one native audio projection. Stop does not rebuild
// the workspace, so release device controls and update timing in place.
var audioPublisher = &sharedLoop{start: func(ctx context.Context) { go publishAudio(ctx) }}

func publishAudio(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		root := captureRoot()
		if !workspacePresent(root) {
			return
		}
		if node := root.Call("querySelector", "[data-audio-routing]"); workspacePresent(node) && !workspacePresent(node.Call("closest", "[hidden]")) {
			var state struct {
				Playing bool                 `json:"playing"`
				Runtime control.AudioRuntime `json:"runtime"`
				Options struct {
					PeriodFrames int `json:"periodFrames"`
				} `json:"options"`
			}
			capture := nativeCapture.Get()
			if readNativeProjection(ctx, "/api/audio/config?runtime=1", &state) && ctx.Err() == nil && node.Get("isConnected").Bool() {
				locked := state.Playing || state.Runtime.Armed || capture.Ready && !capture.Error && capture.State.Active != ""
				node.Set("disabled", locked)
				if notice := root.Call("querySelector", "[data-audio-lock-notice]"); workspacePresent(notice) {
					notice.Set("hidden", !locked)
				}
				for _, row := range control.AudioRuntimeRows(state.Runtime, state.Options.PeriodFrames) {
					if value := root.Call("querySelector", "[data-audio-value="+row.Key+"]"); workspacePresent(value) && value.Get("textContent").String() != row.Value {
						value.Set("textContent", row.Value)
					}
				}
				if warning := root.Call("querySelector", "[data-audio-runtime-warning]"); workspacePresent(warning) {
					message := control.AudioRuntimeWarning(state.Runtime)
					if warning.Get("textContent").String() != message {
						warning.Set("textContent", message)
					}
					warning.Set("hidden", message == "")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
