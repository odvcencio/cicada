//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
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

// capturePublisher publishes nativeCapture and syncs the transport controls.
// The workspace engine (panel application) and the Takes engine (shell) both
// acquire it, so a fresh /studio load has a publisher without the workspace.
var capturePublisher = &sharedLoop{start: func(ctx context.Context) { go publishCapture(ctx) }}

// captureBusy lets the workspace engine hold transport buttons disabled while
// one of its own requests is in flight.
var captureBusy atomic.Pointer[func() bool]

func captureRoot() js.Value {
	document := js.Global().Get("document")
	for _, id := range []string{"cicada-workspace", "studio-shell"} {
		if root := document.Call("getElementById", id); !root.IsNull() {
			return root
		}
	}
	return js.Null()
}

func publishCapture(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := nativeCapture.Get().State
		err := readNativeJSON(ctx, "/api/capture", 2*time.Second, 16<<10, &state)
		if ctx.Err() != nil {
			return
		}
		update := captureUpdate{State: state, Ready: true, Error: err != nil}
		nativeCapture.Set(update)
		if root := captureRoot(); !update.Error && !root.IsNull() {
			busy := false
			if fn := captureBusy.Load(); fn != nil {
				busy = (*fn)()
			}
			syncCaptureControls(root, state, busy)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// actionForms returns the forms under root that selector matches and whose
// action is path. It compares the path alone, not the host (a Studio page only
// holds its own forms) and not the query or fragment: GoSX's managed navigation
// can rewrite the attribute to an absolute URL. recording-levels.test.cjs covers
// the transport buttons after navigation to the Mixer, scene-launch-keys the
// launch button after navigation between views.
func actionForms(root js.Value, selector, path string) []js.Value {
	var forms []js.Value
	list := root.Call("querySelectorAll", selector)
	for i := 0; i < list.Length(); i++ {
		form := list.Index(i)
		action := workspaceActionPath(workspaceAttr(form, "action"))
		if cut := strings.IndexAny(action, "?#"); cut >= 0 {
			action = action[:cut]
		}
		if action == path {
			forms = append(forms, form)
		}
	}
	return forms
}

func syncCaptureControls(root js.Value, state control.CaptureState, busy bool) {
	locked := state.Active != "" && state.Capture != nil && (state.Capture.Recording || state.Capture.Completed)
	root.Call("setAttribute", "data-recording-transport-locked", fmt.Sprint(locked))
	var buttons []js.Value
	for _, form := range actionForms(root, "form", "/__actions/transport") {
		list := form.Call("querySelectorAll", "button")
		for i := 0; i < list.Length(); i++ {
			buttons = append(buttons, list.Index(i))
		}
	}
	for _, button := range buttons {
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
		} else if !busy && button.Call("hasAttribute", "data-capture-transport-lock").Bool() {
			button.Set("disabled", workspaceAttr(button, "data-capture-transport-lock") == "true")
			button.Call("removeAttribute", "data-capture-transport-lock")
		}
	}
	if hint := root.Call("querySelector", "[data-transport-capture-hint]"); workspacePresent(hint) {
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
