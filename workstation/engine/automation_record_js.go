//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

type recordedCurve struct {
	ID, Path string
	Active   bool
	Monitor  bool
	Invalid  bool
	Prepared bool
	Points   []curveEditorPoint
}
type curveRecordingUI struct {
	errorMessage         string
	editor               *curveEditor
	ctx                  context.Context
	form, slider, status js.Value
	take                 *recordedCurve
	pending              *float64
	saving               bool
	awaitingRefresh      bool
	refreshUncertain     bool
	jobs                 chan string
	historyButtons       []historyButtonState
}

// A structured validation/conflict response is a rejection candidate. A
// timeout, lost response or server failure can arrive after a commit.
type automationActionRejected struct{ message string }

func (e *automationActionRejected) Error() string { return e.message }

type historyButtonState struct {
	button   js.Value
	disabled bool
	form     js.Value
	inert    bool
}

// A successful Keep changes the score before the shell's refreshed forms
// arrive. Prevent Undo/Redo from submitting the prior revision in that gap.
func (r *curveRecordingUI) lockHistory() {
	r.historyButtons = nil
	for _, selector := range []string{"#undo-form button", "#redo-form button"} {
		button := js.Global().Get("document").Call("querySelector", selector)
		if workspacePresent(button) {
			form := button.Call("closest", "form")
			state := historyButtonState{button: button, disabled: button.Get("disabled").Bool(), form: form}
			if workspacePresent(form) {
				state.inert = form.Call("hasAttribute", "inert").Bool()
				form.Call("setAttribute", "inert", "")
				form.Call("setAttribute", "data-history-refresh-pending", "")
			}
			r.historyButtons = append(r.historyButtons, state)
			button.Set("disabled", true)
		}
	}
}

func (r *curveRecordingUI) unlockHistory() {
	for _, state := range r.historyButtons {
		if state.button.Get("isConnected").Bool() {
			state.button.Set("disabled", state.disabled)
		}
		if workspacePresent(state.form) && state.form.Get("isConnected").Bool() {
			state.form.Call("removeAttribute", "data-history-refresh-pending")
			if !state.inert {
				state.form.Call("removeAttribute", "inert")
			}
		}
	}
	r.historyButtons = nil
}

func bindAutomationRecording(u *curveEditor) func() {
	ctx, cancel := context.WithCancel(context.Background())
	r := &curveRecordingUI{editor: u, ctx: ctx, form: u.root.Call("querySelector", ".automation-recording"), jobs: make(chan string, 8)}
	if !workspacePresent(r.form) {
		cancel()
		return func() {}
	}
	r.slider = r.form.Call("querySelector", "[data-automation-record-value]")
	r.status = r.form.Call("querySelector", "[data-automation-record-status]")
	u.listen(r.form, "click", func(e js.Value) {
		button := e.Get("target").Call("closest", "[data-automation-record-command]")
		if !workspacePresent(button) || r.saving {
			return
		}
		command := workspaceAttr(button, "data-automation-record-command")
		if command == "start" && hasUnsavedWork() {
			r.status.Set("textContent", "Save or discard your pending edits before recording automation.")
			return
		}
		if monitor := workspaceField(r.form, "monitor"); command == "start" && workspacePresent(monitor) && monitor.Get("checked").Bool() {
			command = "prepare"
		}
		r.errorMessage = ""
		r.saving = true
		if command == "keep" {
			r.lockHistory()
		}
		r.draw()
		select {
		case r.jobs <- command:
		default:
			r.saving = false
			r.unlockHistory()
		}
	})
	if monitor := workspaceField(r.form, "monitor"); workspacePresent(monitor) {
		u.listen(monitor, "change", func(js.Value) { r.draw() })
	}
	u.listen(r.slider, "input", func(js.Value) {
		if r.saving || r.take == nil || !r.take.Active || r.take.Path != u.props.Path {
			return
		}
		value, err := strconv.ParseFloat(r.slider.Get("value").String(), 64)
		if err != nil {
			return
		}
		r.pending = &value
		r.slider.Call("setAttribute", "aria-valuetext", fmt.Sprintf("%g%s", value, u.props.Unit))
		r.form.Call("querySelector", "[data-automation-record-readout]").Set("textContent", fmt.Sprintf("%.3g%s", value, u.props.Unit))
	})
	release := holdUnsavedWork(fmt.Sprintf("automation-record:%p", r), func() bool { return ctx.Err() == nil && r.take != nil })
	go r.run()
	return func() { cancel(); release() }
}

func (r *curveRecordingUI) post(command string, number float64) error {
	version := confirmedWorkspaceVersion.Get()
	values := map[string]string{"csrf_token": workspaceFieldValue(r.form, "csrf_token"), "revision": version.Revision, "source_revision": version.SourceRevision, "command": command, "path": r.editor.props.Path, "number": strconv.FormatFloat(number, 'g', -1, 64)}
	if r.take != nil {
		values["takeId"] = r.take.ID
	}
	if workspaceField(r.form, "replace").Get("checked").Bool() {
		values["replace"] = "on"
	}
	if monitor := workspaceField(r.form, "monitor"); command == "prepare" && workspacePresent(monitor) && monitor.Get("checked").Bool() {
		values["monitor"] = "on"
	}
	data, _ := json.Marshal(values)
	req, err := http.NewRequestWithContext(r.ctx, http.MethodPost, js.Global().Get("location").Get("origin").String()+"/__actions/automation-record", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", values["csrf_token"])
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&result); err != nil {
		return err
	}
	if !result.OK || response.StatusCode >= 400 {
		if result.Message == "" {
			result.Message = "Automation action failed"
		}
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusUnprocessableEntity {
			return &automationActionRejected{message: result.Message}
		}
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

func (r *curveRecordingUI) refresh() {
	var state struct {
		Recording *recordedCurve `json:"recording"`
	}
	if readNativeProjection(r.ctx, "/api/automation/recording", &state) && r.ctx.Err() == nil {
		// Seed once from the authoritative grid-aligned start of a new pass.
		// Never overwrite a movement while later polling updates arrive.
		previousID := ""
		if r.take != nil {
			previousID = r.take.ID
		}
		if state.Recording != nil {
			value, seed := recordingSeed(previousID, state.Recording.ID, state.Recording.Path, r.editor.props.Path, state.Recording.Active, r.pending != nil, state.Recording.Points)
			if seed {
				r.slider.Set("value", strconv.FormatFloat(value, 'f', -1, 64))
				r.slider.Call("setAttribute", "aria-valuetext", fmt.Sprintf("%g%s", value, r.editor.props.Unit))
				r.form.Call("querySelector", "[data-automation-record-readout]").Set("textContent", fmt.Sprintf("%.3g%s", value, r.editor.props.Unit))
			}
		}
		r.take = state.Recording
	}
	r.draw()
}

func (r *curveRecordingUI) run() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	r.refresh()
	poll := time.Now()
	for {
		select {
		case <-r.ctx.Done():
			return
		case command := <-r.jobs:
			keepVersion := confirmedWorkspaceVersion.Get()
			keepID := ""
			if command == "keep" && r.take != nil {
				keepID = r.take.ID
			}
			if command == "keep" && r.pending != nil {
				v := *r.pending
				r.pending = nil
				if err := r.post("value", v); err != nil {
					r.saving = false
					r.unlockHistory()
					r.errorMessage = err.Error()
					r.draw()
					continue
				}
			}
			err := r.post(command, 0)
			if r.ctx.Err() != nil {
				return
			}
			r.saving = false
			r.refresh()
			if err != nil {
				var rejected *automationActionRejected
				var state struct {
					Revision string `json:"revision"`
				}
				var retained struct {
					Recording *recordedCurve `json:"recording"`
				}
				verified := command == "keep" && keepVersion.Revision != "" && keepID != "" && readNativeProjection(r.ctx, "/api/state", &state) && state.Revision == keepVersion.Revision && readNativeProjection(r.ctx, "/api/automation/recording", &retained) && retained.Recording != nil && retained.Recording.ID == keepID
				unchanged := verified && errors.As(err, &rejected)
				if command != "keep" || unchanged {
					r.unlockHistory()
				} else {
					r.awaitingRefresh = true
					r.refreshUncertain = true
				}
				r.errorMessage = err.Error()
				r.draw()
			} else if command == "keep" {
				r.awaitingRefresh = true
				r.draw()
				workspaceRefreshRequested.Update(func(n uint64) uint64 { return n + 1 })
			}
		case <-ticker.C:
			if r.pending != nil && !r.saving {
				v := *r.pending
				r.pending = nil
				if err := r.post("value", v); err != nil {
					if r.ctx.Err() != nil {
						return
					}
					r.refresh()
					r.errorMessage = err.Error()
					r.draw()
				}
			}
			if time.Since(poll) > 500*time.Millisecond {
				r.refresh()
				poll = time.Now()
			}
		}
	}
}

func (r *curveRecordingUI) draw() {
	if r.ctx.Err() != nil || !r.form.Get("isConnected").Bool() {
		return
	}
	active := r.take != nil && r.take.Active && r.take.Path == r.editor.props.Path
	r.slider.Set("disabled", !active || r.saving)
	if monitor := workspaceField(r.form, "monitor"); workspacePresent(monitor) {
		monitor.Set("disabled", r.saving || r.take != nil)
		if r.take != nil {
			monitor.Set("checked", r.take.Monitor)
		}
	}
	capture := nativeCapture.Get()
	captureActive := capture.Ready && !capture.Error && capture.State.Active != ""
	monitor := workspaceField(r.form, "monitor")
	prepare := workspacePresent(monitor) && monitor.Get("checked").Bool()
	for _, command := range []string{"start", "keep", "discard"} {
		button := r.form.Call("querySelector", "[data-automation-record-command="+command+"]")
		button.Set("disabled", r.saving || r.awaitingRefresh || (command == "start" && (r.take != nil || prepare && captureActive)) || (command != "start" && r.take == nil) || (command == "keep" && (captureActive || r.take != nil && (r.take.Invalid || r.take.Prepared))))
		if command == "start" {
			label := "Record movements"
			if monitor := workspaceField(r.form, "monitor"); workspacePresent(monitor) && monitor.Get("checked").Bool() {
				label = "Prepare linked recording"
			}
			button.Set("textContent", label)
		}
		if command == "discard" {
			label := "Discard pass"
			if r.take != nil && r.take.Prepared {
				label = "Cancel preparation"
			}
			button.Set("textContent", label)
		}
	}
	message := "Ready. Start playback, then record a movement."
	if r.take == nil && prepare {
		message = "Prepare before arming or recording the destination audio track."
		if captureActive {
			message = "Finish the current take before preparing linked recording."
		}
	}
	if r.take != nil {
		mode := "Retained pass"
		if r.take.Active {
			mode = "Recording"
		}
		message = fmt.Sprintf("%s · %s · %d points. Keep or discard this pass.", mode, r.take.Path, len(r.take.Points))
		if r.take.Monitor {
			message += " Live input amp linked for this pass."
		}
		if captureActive {
			message += " Stop and save the audio take before keeping the curve."
		}
		if r.take.Invalid {
			message = "This linked pass was interrupted and cannot be kept. Discard the pass; the audio take remains separate."
		}
		if r.take.Prepared {
			message = "Linked recording prepared. Use Record take in Session on the destination track; movements begin with recording."
		}
	}
	if r.saving {
		message = "Saving automation…"
	}
	if r.errorMessage != "" {
		message = r.errorMessage
	}
	if r.awaitingRefresh {
		message = "Curve kept. Waiting for refreshed controls. If they do not update, save or copy any unsaved edits and reload Studio."
		if r.refreshUncertain {
			message = "Could not confirm whether the curve was kept. Save or copy any unsaved edits and reload Studio before using Undo or Redo."
		}
	}
	state := "idle"
	if r.take != nil {
		state = "pending"
		if r.take.Active {
			state = "recording"
		}
		if r.take.Prepared {
			state = "prepared"
		}
	}
	if r.saving {
		state = "saving"
	}
	r.form.Call("setAttribute", "data-automation-record-state", state)
	if r.status.Get("textContent").String() != message {
		r.status.Set("textContent", message)
	}
	graph := r.editor.root.Call("querySelector", "[data-automation-curve]")
	line := graph.Call("querySelector", "[data-automation-record-line]")
	if !workspacePresent(line) {
		line = js.Global().Get("document").Call("createElementNS", "http://www.w3.org/2000/svg", "path")
		line.Call("setAttribute", "data-automation-record-line", "")
		line.Call("setAttribute", "stroke", "#e6ad64")
		line.Call("setAttribute", "stroke-width", "3")
		line.Call("setAttribute", "fill", "none")
		graph.Call("appendChild", line)
	}
	var path strings.Builder
	if r.take != nil && r.take.Path == r.editor.props.Path {
		for i, p := range r.take.Points {
			op := "L"
			if i == 0 {
				op = "M"
			}
			fmt.Fprintf(&path, "%s%.3f %.3f", op, r.editor.x(p.Tick), r.editor.y(p.Value))
		}
	}
	line.Call("setAttribute", "d", path.String())
}
