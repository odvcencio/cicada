//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/signal"
)

func mountTakes(host enginewasm.Context) (enginewasm.Handle, error) {
	var props struct {
		HasAudioTracks bool `json:"hasAudioTracks"`
	}
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	root := host.Mount()
	if root.IsNull() {
		return nil, fmt.Errorf("take panel is missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var closed atomic.Bool
	status := signal.New("Refreshing native capture…")
	watch := signal.Watch(func() {
		if !closed.Load() {
			root.Call("querySelector", "[data-capture-status]").Set("textContent", status.Get())
		}
	})
	audio := root.Call("querySelector", "audio")
	var audioError js.Func
	if !audio.IsNull() {
		audioError = js.FuncOf(func(js.Value, []js.Value) any {
			if !closed.Load() {
				status.Set("Sample preview failed. Check the retained take and try again.")
			}
			return nil
		})
		audio.Call("addEventListener", "error", audioError)
	}
	go func() {
		ticker := time.NewTicker(300 * time.Millisecond)
		defer ticker.Stop()
		client := &http.Client{Timeout: 2 * time.Second}
		for {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/capture", nil)
			if err != nil {
				return
			}
			response, err := client.Do(request)
			if ctx.Err() != nil {
				if response != nil {
					response.Body.Close()
				}
				return
			}
			var state control.CaptureState
			if err == nil {
				err = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&state)
				response.Body.Close()
				if response.StatusCode != 200 {
					err = fmt.Errorf("capture service unavailable")
				}
			}
			if err != nil {
				status.Set("Capture status is unavailable. Reload before recording.")
			} else if !closed.Load() {
				active, recording := state.Active != "", state.Capture != nil && state.Capture.Recording
				buttons := root.Call("querySelectorAll", "[data-capture-control]")
				for i := 0; i < buttons.Length(); i++ {
					b := buttons.Index(i)
					disabled := false
					switch b.Call("getAttribute", "data-capture-control").String() {
					case "arm":
						disabled = active || !props.HasAudioTracks
					case "start":
						disabled = !active || recording
					case "stop":
						disabled = !active
					default:
						disabled = active
					}
					b.Set("disabled", disabled)
				}
				text := "No active capture. Enable input in Audio, then arm an audio track."
				if active {
					text = "Armed take " + state.Active + ". Press Record for the one-bar count-in."
				}
				if recording {
					c := state.Capture
					text = fmt.Sprintf("Recording %s · %d frames saved", state.Active, c.WrittenFrames)
					if state.Rate > 0 {
						text = fmt.Sprintf("Recording %s · %.1f seconds saved", state.Active, float64(c.WrittenFrames)/float64(state.Rate))
					}
					if c.RemainingFrames > 0 && state.Rate > 0 {
						text = fmt.Sprintf("Count-in · %.1f seconds", float64(c.RemainingFrames)/float64(state.Rate))
					}
				}
				if state.Capture != nil && state.Capture.Incomplete {
					text += " · incomplete input; the durable take will be retained"
				}
				if state.Capture != nil && state.Capture.Error != "" {
					text += " · " + state.Capture.Error
				}
				status.Set(text)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return enginewasm.HandleFunc(func() {
		if closed.Swap(true) {
			return
		}
		cancel()
		watch.Dispose()
		if !audio.IsNull() {
			audio.Call("removeEventListener", "error", audioError)
			audioError.Release()
			audio.Call("pause")
			audio.Call("removeAttribute", "src")
			audio.Call("load")
		}
	}), nil
}
