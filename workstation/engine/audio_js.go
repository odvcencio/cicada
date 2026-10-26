//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strconv"
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
		node := root.Call("querySelector", "[data-audio-routing]")
		guitar := root.Call("querySelector", "[data-guitar-monitor]")
		if workspacePresent(guitar) || workspacePresent(node) && !workspacePresent(node.Call("closest", "[hidden]")) {
			var state struct {
				Playing bool                 `json:"playing"`
				Runtime control.AudioRuntime `json:"runtime"`
				Options struct {
					PeriodFrames int     `json:"periodFrames"`
					AmpEnabled   bool    `json:"ampEnabled"`
					AmpDrive     float64 `json:"ampDrive"`
					AmpTone      float64 `json:"ampTone"`
					AmpOutput    float64 `json:"ampOutput"`
					AmpCabinet   int     `json:"ampCabinet"`
				} `json:"options"`
			}
			capture := nativeCapture.Get()
			if readNativeProjection(ctx, "/api/audio/config?runtime=1", &state) && ctx.Err() == nil {
				locked := state.Playing || state.Runtime.Armed || capture.Ready && !capture.Error && capture.State.Active != ""
				if workspacePresent(node) && node.Get("isConnected").Bool() {
					node.Set("disabled", locked)
				}
				if workspacePresent(guitar) && guitar.Get("isConnected").Bool() {
					for name, value := range map[string]string{
						"ampDrive":   strconv.FormatFloat(state.Options.AmpDrive, 'f', -1, 64),
						"ampTone":    strconv.FormatFloat(state.Options.AmpTone, 'f', -1, 64),
						"ampOutput":  strconv.FormatFloat(state.Options.AmpOutput, 'f', -1, 64),
						"ampCabinet": strconv.Itoa(state.Options.AmpCabinet),
					} {
						if input := guitar.Call("querySelector", "input[name="+name+"]"); workspacePresent(input) {
							input.Set("value", value)
						}
					}
					if button := guitar.Call("querySelector", "button[type=submit]"); workspacePresent(button) {
						button.Set("disabled", !state.Options.AmpEnabled)
					}
					cab := "Unknown"
					if state.Options.AmpCabinet >= 0 && state.Options.AmpCabinet < 3 {
						cab = []string{"Open", "Warm", "Bright"}[state.Options.AmpCabinet]
					}
					if summary := root.Call("querySelector", "[data-guitar-monitor-summary]"); workspacePresent(summary) {
						summary.Set("textContent", fmt.Sprintf("Saved monitor sound: drive %.2f, tone %.2f, output %.1f dB, %s cabinet. Takes stay dry. Apply this sound to a track to hear it on playback; reapply after changing the monitor sound.", state.Options.AmpDrive, state.Options.AmpTone, state.Options.AmpOutput, cab))
					}
				}
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
