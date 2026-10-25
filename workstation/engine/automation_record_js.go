//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	jobs                 chan string
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
		r.errorMessage = ""
		r.saving = true
		r.draw()
		select {
		case r.jobs <- command:
		default:
			r.saving = false
		}
	})
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
		return fmt.Errorf("%s", result.Message)
	}
	return nil
}

func (r *curveRecordingUI) refresh() {
	var state struct {
		Recording *recordedCurve `json:"recording"`
	}
	if readNativeProjection(r.ctx, "/api/automation/recording", &state) && r.ctx.Err() == nil {
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
			if command == "keep" && r.pending != nil {
				v := *r.pending
				r.pending = nil
				if err := r.post("value", v); err != nil {
					r.saving = false
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
				r.errorMessage = err.Error()
				r.draw()
			} else if command == "keep" {
				r.status.Set("textContent", "Recorded curve kept. Undo restores the previous curve.")
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
	for _, command := range []string{"start", "keep", "discard"} {
		button := r.form.Call("querySelector", "[data-automation-record-command="+command+"]")
		button.Set("disabled", r.saving || (command == "start" && r.take != nil) || (command != "start" && r.take == nil))
	}
	message := "Ready. Start playback, then record a movement."
	if r.take != nil {
		mode := "Retained pass"
		if r.take.Active {
			mode = "Recording"
		}
		message = fmt.Sprintf("%s · %s · %d points. Keep or discard this pass.", mode, r.take.Path, len(r.take.Points))
	}
	if r.saving {
		message = "Saving automation…"
	}
	if r.errorMessage != "" {
		message = r.errorMessage
	}
	state := "idle"
	if r.take != nil {
		state = "pending"
		if r.take.Active {
			state = "recording"
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
