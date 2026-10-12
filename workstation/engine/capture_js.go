//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx/signal"
)

type captureUpdate struct {
	State control.CaptureState
	Ready bool
	Error bool
}

// One native projection drives take status, metering, and transport admission
// on every panel. Navigating away from Session does not lose recording truth.
var nativeCapture = signal.New(captureUpdate{})

func (u *workspaceUI) watchCapture() {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/capture", nil)
		if err != nil {
			return
		}
		response, err := client.Do(request)
		state := nativeCapture.Get().State
		if response != nil {
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("capture service unavailable")
			} else {
				err = json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&state)
			}
			response.Body.Close()
		}
		if u.closed.Load() || u.ctx.Err() != nil {
			return
		}
		update := captureUpdate{State: state, Ready: true, Error: err != nil}
		nativeCapture.Set(update)
		if !update.Error {
			u.syncCaptureControls(state)
		}
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (u *workspaceUI) syncCaptureControls(state control.CaptureState) {
	locked := state.Active != "" && state.Capture != nil && (state.Capture.Recording || state.Capture.Completed)
	u.root.Call("setAttribute", "data-recording-transport-locked", fmt.Sprint(locked))
	buttons := u.root.Call("querySelectorAll", "form[action='/__actions/transport'] button")
	for i := 0; i < buttons.Length(); i++ {
		button := buttons.Index(i)
		action := ""
		if workspaceAttr(button, "name") == "action" {
			action = workspaceAttr(button, "value")
		} else if hidden := button.Call("closest", "form").Call("querySelector", "input[name=action]"); workspacePresent(hidden) {
			action = hidden.Get("value").String()
		}
		if action == "stop" {
			if locked {
				if !button.Call("hasAttribute", "data-capture-stop-label").Bool() {
					button.Call("setAttribute", "data-capture-stop-label", button.Get("textContent").String())
				}
				button.Set("textContent", "Stop & save take")
			} else if button.Call("hasAttribute", "data-capture-stop-label").Bool() {
				button.Set("textContent", workspaceAttr(button, "data-capture-stop-label"))
				button.Call("removeAttribute", "data-capture-stop-label")
			}
			continue
		}
		if locked {
			if !button.Call("hasAttribute", "data-capture-transport-lock").Bool() {
				button.Call("setAttribute", "data-capture-transport-lock", fmt.Sprint(button.Get("disabled").Bool()))
			}
			button.Set("disabled", true)
		} else if !u.busy.Load() && button.Call("hasAttribute", "data-capture-transport-lock").Bool() {
			button.Set("disabled", workspaceAttr(button, "data-capture-transport-lock") == "true")
			button.Call("removeAttribute", "data-capture-transport-lock")
		}
	}
	if hint := u.root.Call("querySelector", "[data-transport-capture-hint]"); workspacePresent(hint) {
		hint.Set("hidden", !locked)
	}
}

func renderInputMeter(root js.Value, update captureUpdate) {
	node := root.Call("querySelector", "[data-input-meter]")
	if !workspacePresent(node) {
		return
	}
	m := update.State.Input
	status, hint := m.Status()
	if update.Error {
		m.Active = false
		status, hint = "Input status unavailable", "Reload to check your microphone connection."
	}
	node.Call("setAttribute", "data-input-active", fmt.Sprint(m.Active))
	node.Call("setAttribute", "data-input-clipped", fmt.Sprint(m.Clipped))
	node.Call("querySelector", "[data-input-status]").Set("textContent", status)
	node.Call("querySelector", "[data-input-hint]").Set("textContent", hint)
	peak := "Peak —"
	if max(m.HeldPeakL, m.HeldPeakR) > 0 {
		peak = "Peak " + control.MeterText(max(m.HeldPeakL, m.HeldPeakR))
	}
	node.Call("querySelector", "[data-input-peak]").Set("textContent", peak)
	for _, channel := range []string{"L", "R"} {
		row := node.Call("querySelector", "[data-input-channel="+channel+"]")
		row.Set("hidden", channel == "R" && m.Channels < 2)
		row.Call("querySelector", "[data-input-label]").Set("textContent", map[bool]string{true: channel, false: "Input"}[m.Channels >= 2])
		level, held := m.PeakL, m.HeldPeakL
		if channel == "R" {
			level, held = m.PeakR, m.HeldPeakR
		}
		if !m.Active {
			level = 0
		}
		db, heldDB := math.Min(0, control.MeterDB(level)), math.Min(0, control.MeterDB(held))
		bar := row.Call("querySelector", "[role=meter]")
		bar.Call("setAttribute", "aria-valuenow", fmt.Sprintf("%.1f", db))
		bar.Call("setAttribute", "aria-valuetext", control.MeterText(level))
		fill := row.Call("querySelector", "[data-input-fill]")
		fill.Get("style").Set("width", fmt.Sprintf("%.2f%%", (db+60)/60*100))
		tone := "normal"
		if db >= -6 {
			tone = "loud"
		}
		if level >= .9999 {
			tone = "clip"
		}
		fill.Call("setAttribute", "data-input-tone", tone)
		marker := row.Call("querySelector", "[data-input-hold]")
		marker.Set("hidden", held <= 0)
		marker.Get("style").Set("left", fmt.Sprintf("%.2f%%", (heldDB+60)/60*100))
	}
}
