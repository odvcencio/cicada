//go:build js && wasm

package main

import (
	"fmt"
	"sync/atomic"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/signal"
)

func mountTakes(host enginewasm.Context) (enginewasm.Handle, error) {
	var props struct {
		HasAudioTracks bool   `json:"hasAudioTracks"`
		Active         string `json:"active"`
		Timeline       bool   `json:"timeline"`
	}
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	root := host.Mount()
	if root.IsNull() {
		return nil, fmt.Errorf("take panel is missing")
	}
	var closed atomic.Bool
	status := signal.New("Refreshing native capture…")
	watch := signal.Watch(func() {
		if !closed.Load() {
			root.Call("querySelector", "[data-capture-status]").Set("textContent", status.Get())
		}
	})
	fullSong := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if closed.Load() || len(args) == 0 {
			return nil
		}
		target := args[0].Get("target")
		if target.Get("closest").Type() != js.TypeFunction {
			return nil
		}
		button := target.Call("closest", "[data-capture-full-song]")
		if !workspacePresent(button) || button.Get("disabled").Bool() {
			return nil
		}
		form := button.Call("closest", "form")
		for name, value := range map[string]string{"recordStart": "@1.1.1", "recordEnd": workspaceAttr(button, "data-capture-full-song")} {
			input := form.Call("querySelector", "[name="+name+"]")
			input.Set("value", value)
			input.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		}
		return nil
	})
	root.Call("addEventListener", "click", fullSong)
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
	releaseCapture := capturePublisher.acquire()
	lastActive := props.Active
	captureWatch := signal.Watch(func() {
		update := nativeCapture.Get()
		if !update.Ready || closed.Load() {
			return
		}
		state := update.State
		if update.Error {
			renderInputMeter(root, update)
			status.Set("Capture status is unavailable. Reload before recording.")
		} else if !closed.Load() {
			active, recording := state.Active != "", state.Capture != nil && state.Capture.Recording
			root.Call("setAttribute", "data-capture-active", fmt.Sprint(active))
			if lastActive != "" && state.Active == "" {
				workspaceRefreshRequested.Update(func(n uint64) uint64 { return n + 1 })
			}
			lastActive = state.Active
			buttons := root.Call("querySelectorAll", "[data-capture-control]")
			for i := 0; i < buttons.Length(); i++ {
				b := buttons.Index(i)
				disabled := false
				switch b.Call("getAttribute", "data-capture-control").String() {
				case "arm", "record":
					hasTracks := props.HasAudioTracks
					if track := root.Call("querySelector", "select[name=track]"); workspacePresent(track) {
						hasTracks = track.Get("options").Length() > 0
					}
					disabled = active || !hasTracks
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
			if props.Timeline {
				if node := root.Call("querySelector", "[data-capture-idle]"); workspacePresent(node) {
					text = workspaceAttr(node, "data-capture-idle")
				} else {
					text = "Choose your microphone in Recording setup before recording."
				}
			}
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
			if state.Capture != nil && state.Capture.Completed {
				text = "Saving take and updating your vocal…"
			}
			if state.Loop != nil && active {
				text = fmt.Sprintf("Pass %d of %d · %s", state.Loop.Pass, state.Loop.Passes, text)
			}
			if state.Capture != nil && state.Capture.Incomplete {
				text += " · incomplete input; the durable take will be retained"
			}
			if state.Capture != nil && state.Capture.Error != "" {
				text += " · " + state.Capture.Error
			}
			if button := root.Call("querySelector", "[data-capture-full-song]"); workspacePresent(button) {
				button.Set("disabled", active)
			}
			renderInputMeter(root, update)
			fields := root.Call("querySelectorAll", ".vocal-record select, .vocal-record input:not([type=hidden]), [data-vocal-level-control]")
			for i := 0; i < fields.Length(); i++ {
				fields.Index(i).Set("disabled", active)
			}
			status.Set(text)
		}
	})
	return enginewasm.HandleFunc(func() {
		if closed.Swap(true) {
			return
		}
		releaseCapture()
		captureWatch.Dispose()
		watch.Dispose()
		root.Call("removeEventListener", "click", fullSong)
		fullSong.Release()
		if !audio.IsNull() {
			audio.Call("removeEventListener", "error", audioError)
			audioError.Release()
			audio.Call("pause")
			audio.Call("removeAttribute", "src")
			audio.Call("load")
		}
	}), nil
}
