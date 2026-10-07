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
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	"golang.org/x/net/html"
	"m31labs.dev/gosx/client/vm"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/signal"
)

// The workspace is a GoSX engine, using GoSX's resolved-tree differ and DOM
// patch receiver. The server remains responsible for every canonical view and
// revision check; the engine only serializes gestures and retains browser state.
type workspaceProps struct {
	ProjectRevision string `json:"projectRevision"`
	CSRF            string `json:"csrf"`
	Revision        string `json:"revision"`
}

type workspaceProjection struct {
	ProjectRevision string `json:"projectRevision"`
	FileRevision    string `json:"fileRevision"`
	HTML            string `json:"html"`
	Revision        string `json:"revision"`
	Location        string `json:"location"`
	Title           string `json:"title"`
	WriteRevision   string `json:"writeRevision"`
	RefreshRequired bool   `json:"refreshRequired"`
}

type workspaceControl struct {
	node      js.Value
	signature string
}

type workspaceJob struct {
	method, target, confirmed string
	values                    map[string]string
	form                      js.Value
	controls                  []workspaceControl
	refresh                   bool
	pitchDelta                int
	focusRow, focusStep       string
	pendingCell               js.Value
}

type workspaceUI struct {
	root, patch js.Value
	props       workspaceProps
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
	blocked     atomic.Bool
	jobs        chan workspaceJob
	events      []workspaceEvent
	revision    *signal.Signal[string]
	status      *signal.Signal[string]
	tool        *signal.Signal[string]
	drafts      map[string]workspaceDraft
	scrolls     []workspaceScroll
	editors     map[string]workspaceEditorBaseline
}

type workspaceEditorBaseline struct {
	raw, rendered string
}

type workspaceDraft struct {
	value    string
	checked  *bool
	selected map[string]bool
}

type workspaceEvent struct {
	target   js.Value
	name     string
	callback js.Func
}

const workspaceEditable = "input:not([type=hidden]):not([type=file]), textarea, select"

// The receiver expects a string even for an empty attribute/text value. The
// framework's compact PatchOp JSON omits empty Text, so make that wire field
// explicit without changing the framework's typed diff or patch receiver.
type workspacePatch struct {
	vm.PatchOp
	Text string `json:"text"`
}

func workspaceEncodePatches(ops []vm.PatchOp) ([]byte, error) {
	patches := make([]workspacePatch, len(ops))
	for i, op := range ops {
		patches[i] = workspacePatch{PatchOp: op, Text: op.Text}
	}
	return json.Marshal(patches)
}

func mountWorkspace(host enginewasm.Context) (enginewasm.Handle, error) {
	var props workspaceProps
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	document := js.Global().Get("document")
	root := document.Call("getElementById", "cicada-workspace")
	if !workspacePresent(root) || !workspacePresent(root.Get("firstElementChild")) {
		return nil, fmt.Errorf("workspace projection is missing")
	}
	gosx := js.Global().Get("__gosx")
	patch := js.Undefined()
	if workspacePresent(gosx) && workspacePresent(gosx.Get("host")) {
		receiver := gosx.Get("host").Get("patch")
		if workspacePresent(receiver) {
			patch = receiver.Get("applyJSON")
		}
	}
	if patch.Type() != js.TypeFunction {
		patch = js.Global().Get("__gosx_apply_patches")
	}
	if patch.Type() != js.TypeFunction {
		return nil, fmt.Errorf("GoSX DOM patch receiver is unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &workspaceUI{root: root, patch: patch, props: props, ctx: ctx, cancel: cancel, jobs: make(chan workspaceJob, 128), revision: signal.New(props.Revision), status: signal.New(""), tool: signal.New("select"), drafts: map[string]workspaceDraft{}, editors: map[string]workspaceEditorBaseline{}}
	u.seedEditorBaseline()
	watch := signal.Watch(func() {
		if u.closed.Load() {
			return
		}
		if node := root.Call("querySelector", "[data-workspace-status]"); workspacePresent(node) {
			node.Set("textContent", u.status.Get())
		}
		if node := root.Call("querySelector", "[data-workspace-retry]"); workspacePresent(node) {
			node.Set("hidden", !u.blocked.Load())
		}
		u.syncTools()
	})
	seenRefresh := workspaceRefreshRequested.Get()
	refreshWatch := signal.Watch(func() {
		requested := workspaceRefreshRequested.Get()
		if requested == seenRefresh || u.closed.Load() {
			return
		}
		seenRefresh = requested
		location, _ := url.Parse(workspaceLocation())
		u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + location.RawQuery, form: js.Undefined()})
	})
	u.listen(document, "submit", u.submit)
	u.listen(document, "click", u.click)
	u.listen(document, "dblclick", u.doubleClick)
	u.listen(document, "keydown", u.keyDown)
	u.scrolls = workspaceScrolls(root.Get("firstElementChild"))
	u.listen(document, "scroll", u.scrolled)
	u.revealInitialNote()
	root.Call("setAttribute", "data-workspace-reactive", "ready")
	confirmedWorkspaceRevision.Set(props.Revision)
	go u.run()
	go u.watchFiles()
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		u.cancel()
		root.Call("removeAttribute", "data-workspace-reactive")
		for _, event := range u.events {
			event.target.Call("removeEventListener", event.name, event.callback, true)
			event.callback.Release()
		}
		watch.Dispose()
		refreshWatch.Dispose()
		u.clearQueue()
		pending := root.Call("querySelectorAll", "[data-workspace-pending]")
		for i := 0; i < pending.Length(); i++ {
			pending.Index(i).Call("removeAttribute", "data-workspace-pending")
		}
	}), nil
}

func workspacePresent(value js.Value) bool { return !value.IsNull() && !value.IsUndefined() }

func workspaceAttr(node js.Value, name string) string {
	if !workspacePresent(node) || node.Get("getAttribute").Type() != js.TypeFunction {
		return ""
	}
	value := node.Call("getAttribute", name)
	if value.IsNull() {
		return ""
	}
	return value.String()
}

func workspaceString(node js.Value, name string) string {
	value := node.Get(name)
	if value.IsNull() || value.IsUndefined() {
		return ""
	}
	return value.String()
}

func (u *workspaceUI) listen(target js.Value, name string, fn func(js.Value)) {
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed.Load() && len(args) > 0 {
			fn(args[0])
		}
		return nil
	})
	// Register capture listeners after bootstrap; stopImmediatePropagation keeps
	// its bubbling form/navigation handler from submitting the same gesture.
	target.Call("addEventListener", name, callback, true)
	u.events = append(u.events, workspaceEvent{target, name, callback})
}

func (u *workspaceUI) owned(node js.Value) bool {
	return workspacePresent(node) && u.root.Call("contains", node).Bool()
}

func workspaceNative(node js.Value) bool {
	return node.Call("hasAttribute", "data-gosx-native").Bool() || strings.EqualFold(workspaceAttr(node, "data-gosx-managed"), "false") || strings.EqualFold(workspaceAttr(node, "data-gosx-link"), "false") || strings.EqualFold(workspaceAttr(node, "data-gosx-form"), "false")
}

func workspaceEngineOwned(node js.Value) bool {
	owner := node.Call("closest", "[data-gosx-engine], [data-gosx-island], [data-gosx-runtime-surface]")
	return workspacePresent(owner) && workspaceAttr(owner, "data-gosx-engine") != "CicadaTakes"
}

func workspaceLocation() string {
	location := js.Global().Get("location")
	return location.Get("pathname").String() + location.Get("search").String() + location.Get("hash").String()
}

func workspaceURL(raw string) (*url.URL, bool) {
	base, err := url.Parse(js.Global().Get("location").Get("href").String())
	if err != nil {
		return nil, false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	parsed = base.ResolveReference(parsed)
	return parsed, parsed.Scheme == base.Scheme && parsed.Host == base.Host && parsed.User == nil
}

func (u *workspaceUI) submit(event js.Value) {
	if event.Get("defaultPrevented").Bool() {
		return
	}
	form := event.Get("target")
	if !u.owned(form) || workspaceNative(form) || strings.ToUpper(workspaceString(form, "tagName")) != "FORM" {
		return
	}
	if target := workspaceAttr(form, "target"); target != "" && target != "_self" {
		return
	}
	submitter := event.Get("submitter")
	// HTML named controls can shadow form.action/form.method. Read markup
	// attributes so a button named action can never replace the endpoint.
	method, target := workspaceAttr(form, "method"), workspaceAttr(form, "action")
	if method == "" {
		method = http.MethodGet
	}
	if target == "" {
		target = workspaceLocation()
	}
	if workspacePresent(submitter) {
		if workspaceNative(submitter) {
			return
		}
		if value := workspaceAttr(submitter, "formmethod"); value != "" {
			method = value
		}
		if value := workspaceAttr(submitter, "formaction"); value != "" {
			target = value
		}
		if value := workspaceAttr(submitter, "formtarget"); value != "" && value != "_self" {
			return
		}
	}
	parsed, safe := workspaceURL(target)
	method = strings.ToUpper(method)
	if !safe || (method != http.MethodPost && method != http.MethodGet) || (method == http.MethodPost && !strings.HasPrefix(parsed.Path, "/__actions/")) {
		return
	}
	if workspaceEngineOwned(form) && parsed.Path != "/__actions/source" {
		return
	}
	// File uploads keep GoSX's native multipart transport.
	files := form.Call("querySelectorAll", "input[type=file]")
	if files.Length() > 0 {
		return
	}
	values := map[string]string{}
	entries := js.Global().Get("FormData").New(form).Call("entries")
	for {
		entry := entries.Call("next")
		if entry.Get("done").Bool() {
			break
		}
		pair := entry.Get("value")
		values[pair.Index(0).String()] = pair.Index(1).String()
	}
	if workspacePresent(submitter) && workspaceString(submitter, "name") != "" {
		values[workspaceString(submitter, "name")] = workspaceString(submitter, "value")
	}
	if method == http.MethodGet {
		current, _ := url.Parse(workspaceLocation())
		query := parsed.Query()
		for name, value := range values {
			query.Set(name, value)
		}
		if parsed.Path != current.Path || query.Get("panel") != current.Query().Get("panel") {
			return
		}
		event.Call("preventDefault")
		event.Call("stopImmediatePropagation")
		u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + query.Encode(), form: form, controls: workspaceControls(form)})
		return
	}
	values["__cicada_reactive"] = "1"
	values["__cicada_location"] = workspaceLocation()
	job := workspaceJob{method: http.MethodPost, target: parsed.RequestURI(), values: values, form: form, confirmed: u.revision.Get(), controls: workspaceControls(form)}
	if workspacePresent(submitter) && submitter.Call("matches", ".roll-cell").Bool() {
		job.pendingCell = submitter
	}
	event.Call("preventDefault")
	event.Call("stopImmediatePropagation")
	u.queue(job)
}

func (u *workspaceUI) click(event js.Value) {
	if event.Get("defaultPrevented").Bool() || event.Get("button").Int() != 0 {
		return
	}
	target := event.Get("target")
	if !u.owned(target) || target.Get("closest").Type() != js.TypeFunction {
		return
	}
	modified := event.Get("altKey").Bool() || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("shiftKey").Bool()
	if button := target.Call("closest", "[data-workspace-tool]"); workspacePresent(button) && !modified {
		workspaceStop(event)
		u.tool.Set(workspaceAttr(button, "data-workspace-tool"))
		return
	}
	if cell := target.Call("closest", ".roll-cell"); workspacePresent(cell) && !workspaceNative(cell) && !workspaceEngineOwned(cell) {
		if u.tool.Get() == "select" || modified || event.Get("detail").Int() > 1 {
			workspaceStop(event)
			if !modified && u.tool.Get() == "select" && event.Get("detail").Int() <= 1 {
				u.selectCell(cell, workspaceString(cell, "value"))
			}
			return
		}
	}
	if modified {
		return
	}
	if workspacePresent(target.Call("closest", "[data-workspace-retry]")) {
		event.Call("preventDefault")
		event.Call("stopImmediatePropagation")
		location, _ := url.Parse(workspaceLocation())
		u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + location.RawQuery, form: js.Undefined(), refresh: true})
		return
	}
	anchor := target.Call("closest", "a[href]")
	if !workspacePresent(anchor) || workspaceNative(anchor) || workspaceEngineOwned(anchor) || anchor.Call("hasAttribute", "download").Bool() {
		return
	}
	if value := workspaceAttr(anchor, "target"); value != "" && value != "_self" {
		return
	}
	parsed, safe := workspaceURL(workspaceAttr(anchor, "href"))
	current, _ := url.Parse(workspaceLocation())
	if !safe || parsed.Path != current.Path || parsed.RawQuery == current.RawQuery || parsed.Query().Get("panel") != current.Query().Get("panel") || parsed.Fragment != "" {
		return
	}
	event.Call("preventDefault")
	event.Call("stopImmediatePropagation")
	u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + parsed.RawQuery, form: js.Undefined()})
}

func workspaceStop(event js.Value) {
	event.Call("preventDefault")
	event.Call("stopImmediatePropagation")
}

// Only mounting can reveal an off-screen authored note. Canonical projections
// and subsequent selection deliberately retain the user's viewport.
func (u *workspaceUI) revealInitialNote() {
	grid := u.root.Call("querySelector", ".piano-roll[data-pattern-kind=notes]")
	if !workspacePresent(grid) {
		return
	}
	cell := grid.Call("querySelector", ".roll-cell[data-active=true][data-selected=true]")
	if !workspacePresent(cell) {
		cell = grid.Call("querySelector", ".roll-cell[data-active=true]")
	}
	if !workspacePresent(cell) {
		return
	}
	viewport, note := grid.Call("getBoundingClientRect"), cell.Call("getBoundingClientRect")
	top := viewport.Get("top").Float() + grid.Get("clientTop").Float()
	left := viewport.Get("left").Float() + grid.Get("clientLeft").Float()
	height, width := grid.Get("clientHeight").Float(), grid.Get("clientWidth").Float()
	if height <= 0 || width <= 0 {
		return
	}
	dy, dx := 0.0, 0.0
	if note.Get("top").Float() < top {
		dy = note.Get("top").Float() - top - 8
	} else if note.Get("bottom").Float() > top+height {
		dy = note.Get("bottom").Float() - top - height + 8
	}
	if note.Get("left").Float() < left {
		dx = note.Get("left").Float() - left - 8
	} else if note.Get("right").Float() > left+width {
		dx = note.Get("right").Float() - left - width + 8
	}
	if dy == 0 && dx == 0 {
		return
	}
	grid.Set("scrollTop", grid.Get("scrollTop").Float()+dy)
	grid.Set("scrollLeft", grid.Get("scrollLeft").Float()+dx)
	for _, scroll := range u.scrolls {
		if scroll.node.Equal(grid) {
			return
		}
	}
	u.scrolls = append(u.scrolls, workspaceScroll{node: grid})
}

func (u *workspaceUI) syncTools() {
	mode := u.tool.Get()
	group := u.root.Call("querySelector", "[data-workspace-tools]")
	if !workspacePresent(group) {
		return
	}
	group.Set("hidden", false)
	buttons := group.Call("querySelectorAll", "[data-workspace-tool]")
	for i := 0; i < buttons.Length(); i++ {
		button := buttons.Index(i)
		button.Call("setAttribute", "aria-pressed", strconv.FormatBool(workspaceAttr(button, "data-workspace-tool") == mode))
	}
	if help := u.root.Call("querySelector", "[data-workspace-help]"); workspacePresent(help) {
		text := "Select a cell to inspect its step. Double-click an empty cell to add a note. B switches tools; Delete clears the focused step."
		if mode == "draw" {
			text = "Click a cell to write or erase a note. B switches to Select. Arrow keys select steps or transpose the selected note; Delete clears a step."
		}
		help.Set("textContent", text)
	}
}

func (u *workspaceUI) doubleClick(event js.Value) {
	if u.tool.Get() != "select" || event.Get("button").Int() != 0 || event.Get("altKey").Bool() || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("shiftKey").Bool() {
		return
	}
	target := event.Get("target")
	if !u.owned(target) || target.Get("closest").Type() != js.TypeFunction {
		return
	}
	cell := target.Call("closest", ".roll-cell")
	if !workspacePresent(cell) || workspaceNative(cell) || workspaceEngineOwned(cell) {
		return
	}
	workspaceStop(event)
	if workspaceAttr(cell, "data-active") != "true" {
		form := cell.Call("closest", "form")
		if workspacePresent(form) {
			form.Call("requestSubmit", cell)
		}
	}
}

func workspaceField(form js.Value, name string) js.Value {
	return form.Call("querySelector", "[name='"+name+"']")
}

func workspaceFieldValue(form js.Value, name string) string {
	if field := workspaceField(form, name); workspacePresent(field) {
		return workspaceString(field, "value")
	}
	return ""
}

func (u *workspaceUI) selectCell(cell js.Value, step string) {
	form, grid := cell.Call("closest", "form"), cell.Call("closest", ".piano-roll")
	number, err := strconv.Atoi(step)
	if err != nil || !workspacePresent(form) || !workspacePresent(grid) || number < 1 || number > form.Call("querySelectorAll", ".roll-cell").Length() {
		return
	}
	location, _ := url.Parse(workspaceLocation())
	query := location.Query()
	query.Set("panel", "patterns")
	query.Set("pattern", workspaceAttr(grid, "data-pattern-id"))
	query.Set("step", step)
	query.Set("octave", workspaceFieldValue(form, "octave"))
	if lane := workspaceFieldValue(form, "lane"); lane != "" {
		query.Set("lane", lane)
	} else {
		query.Del("lane")
	}
	row := cell.Call("closest", ".roll-row")
	u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + query.Encode(), form: js.Undefined(), focusRow: workspaceAttr(row, "data-gosx-key"), focusStep: step, pendingCell: cell})
}

func (u *workspaceUI) editValues() map[string]string {
	return map[string]string{"revision": u.revision.Get(), "csrf": u.props.CSRF, "__cicada_reactive": "1", "__cicada_location": workspaceLocation()}
}

func (u *workspaceUI) keyDown(event js.Value) {
	target := event.Get("target")
	if !u.owned(target) || target.Get("closest").Type() != js.TypeFunction || workspaceNative(target) || workspaceEngineOwned(target) {
		return
	}
	key := event.Get("key").String()
	if key == "Escape" && strings.EqualFold(workspaceString(target, "tagName"), "input") && workspaceString(target, "type") == "number" {
		workspaceStop(event)
		target.Set("value", workspaceString(target, "defaultValue"))
		target.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		return
	}
	if workspacePresent(target.Call("closest", "input, textarea, select, [contenteditable]:not([contenteditable=false]), [role=textbox]")) {
		return
	}
	ctrl, shift, alt := event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool(), event.Get("shiftKey").Bool(), event.Get("altKey").Bool()
	if ctrl && !alt {
		action := ""
		if strings.EqualFold(key, "z") {
			action = "undo"
			if shift {
				action = "redo"
			}
		} else if strings.EqualFold(key, "y") {
			action = "redo"
		}
		if action != "" {
			workspaceStop(event)
			u.queue(workspaceJob{method: http.MethodPost, target: "/__actions/" + action, values: u.editValues(), confirmed: u.revision.Get(), form: js.Undefined()})
		}
		return
	}
	if !ctrl && !alt && strings.EqualFold(key, "b") && workspacePresent(u.root.Call("querySelector", ".piano-roll")) {
		workspaceStop(event)
		mode := "draw"
		if u.tool.Get() == "draw" {
			mode = "select"
		}
		u.tool.Set(mode)
		return
	}
	cell := target.Call("closest", ".roll-cell")
	if !workspacePresent(cell) || ctrl || alt {
		return
	}
	form, grid := cell.Call("closest", "form"), cell.Call("closest", ".piano-roll")
	step := workspaceString(cell, "value")
	number, err := strconv.Atoi(step)
	if err != nil || !workspacePresent(form) || !workspacePresent(grid) {
		return
	}
	switch key {
	case "Enter":
		if u.tool.Get() == "select" {
			workspaceStop(event)
			u.selectCell(cell, step)
		}
	case "ArrowLeft", "ArrowRight":
		workspaceStop(event)
		if key == "ArrowLeft" {
			number--
		} else {
			number++
		}
		u.selectCell(cell, strconv.Itoa(number))
	case "Delete", "Backspace":
		workspaceStop(event)
		values := u.editValues()
		values["action"], values["operation"] = "range", "clear"
		values["pattern"], values["lane"] = workspaceAttr(grid, "data-pattern-id"), workspaceFieldValue(form, "lane")
		values["first"], values["last"], values["target"], values["amount"] = step, step, step, "0"
		values["octave"] = workspaceFieldValue(form, "octave")
		u.queue(workspaceJob{method: http.MethodPost, target: "/__actions/pattern", values: values, confirmed: u.revision.Get(), form: js.Undefined(), pendingCell: cell})
	case "ArrowUp", "ArrowDown":
		if workspaceAttr(cell, "data-selected") != "true" || workspaceAttr(grid, "data-pattern-kind") != "notes" {
			return
		}
		delta := 1
		if shift {
			delta = 12
		}
		if key == "ArrowDown" {
			delta = -delta
		}
		if values, ok := u.transposeValues(workspaceAttr(grid, "data-pattern-id"), step, delta); ok {
			workspaceStop(event)
			u.queue(workspaceJob{method: http.MethodPost, target: "/__actions/pattern", values: values, confirmed: u.revision.Get(), form: js.Undefined(), pitchDelta: delta, pendingCell: cell})
		}
	}
}

func workspaceDefaultValue(control js.Value) string {
	if !workspacePresent(control) {
		return ""
	}
	if strings.EqualFold(workspaceString(control, "tagName"), "select") {
		options := control.Get("options")
		for i := 0; i < options.Length(); i++ {
			option := options.Index(i)
			if option.Get("defaultSelected").Bool() {
				return workspaceString(option, "value")
			}
		}
		if options.Length() > 0 {
			return workspaceString(options.Index(0), "value")
		}
		return ""
	}
	return workspaceString(control, "defaultValue")
}

// Keyboard transposition changes the confirmed note. Unsaved inspector drafts
// retain their own semantic identity and are never bundled into the shortcut.
func (u *workspaceUI) transposeValues(pattern, step string, delta int) (map[string]string, bool) {
	form := u.root.Call("querySelector", "#step-inspector form")
	if !workspacePresent(form) || workspaceFieldValue(form, "pattern") != pattern || workspaceFieldValue(form, "step") != step || workspaceDefaultValue(workspaceField(form, "mode")) != "note" {
		return nil, false
	}
	pitch, err := strconv.Atoi(workspaceDefaultValue(workspaceField(form, "pitch")))
	if err != nil {
		return nil, false
	}
	changed := max(0, min(127, pitch+delta))
	if changed == pitch {
		return nil, false
	}
	values := u.editValues()
	values["action"], values["pattern"], values["step"], values["mode"], values["pitch"] = "step", pattern, step, "note", strconv.Itoa(changed)
	for _, name := range []string{"lane", "octave", "ratchet", "chance", "velocity"} {
		values[name] = workspaceDefaultValue(workspaceField(form, name))
	}
	for _, name := range []string{"accent", "slide"} {
		if control := workspaceField(form, name); workspacePresent(control) && control.Get("defaultChecked").Bool() {
			values[name] = "on"
		}
	}
	return values, true
}

func (u *workspaceUI) focusGesture(job workspaceJob) {
	if job.focusStep == "" && job.pitchDelta == 0 {
		return
	}
	active := js.Global().Get("document").Get("activeElement")
	if workspacePresent(active) && !strings.EqualFold(workspaceString(active, "tagName"), "body") && !workspacePresent(active.Call("closest", ".roll-cell")) {
		return
	}
	var cell js.Value
	if job.pitchDelta != 0 {
		cell = u.root.Call("querySelector", ".roll-cell[data-active=true][value='"+job.values["step"]+"']")
	} else {
		rows := u.root.Call("querySelectorAll", ".roll-row")
		for i := 0; i < rows.Length(); i++ {
			row := rows.Index(i)
			if workspaceAttr(row, "data-gosx-key") == job.focusRow {
				cell = row.Call("querySelector", ".roll-cell[value='"+job.focusStep+"']")
				break
			}
		}
	}
	if workspacePresent(cell) {
		cell.Call("focus", map[string]any{"preventScroll": true})
	}
}

func workspacePending(job workspaceJob, delta int) {
	if !workspacePresent(job.pendingCell) {
		return
	}
	count, _ := strconv.Atoi(workspaceAttr(job.pendingCell, "data-workspace-pending"))
	count += delta
	if count <= 0 {
		job.pendingCell.Call("removeAttribute", "data-workspace-pending")
	} else {
		job.pendingCell.Call("setAttribute", "data-workspace-pending", strconv.Itoa(count))
	}
}

func (u *workspaceUI) queue(job workspaceJob) {
	if u.closed.Load() {
		return
	}
	if u.blocked.Load() && !job.refresh {
		u.status.Set("Refresh the workspace before editing again. Your drafts are retained.")
		return
	}
	workspacePending(job, 1)
	select {
	case u.jobs <- job:
	default:
		workspacePending(job, -1)
		u.blocked.Store(true)
		u.clearQueue()
		u.status.Set("The edit queue is full. Refresh the workspace; drafts are retained.")
	}
}

func (u *workspaceUI) clearQueue() {
	for {
		select {
		case job := <-u.jobs:
			workspacePending(job, -1)
		default:
			return
		}
	}
}

func (u *workspaceUI) run() {
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		select {
		case <-u.ctx.Done():
			return
		case job := <-u.jobs:
			if u.blocked.Load() && !job.refresh {
				workspacePending(job, -1)
				continue
			}
			if job.pitchDelta != 0 {
				values, ok := u.transposeValues(job.values["pattern"], job.values["step"], job.pitchDelta)
				if !ok {
					workspacePending(job, -1)
					continue
				}
				job.values = values
				job.confirmed = u.revision.Get()
			}
			if revision, exists := job.values["revision"]; exists && revision == job.confirmed {
				// Only advance our own queued gestures. A form already stale when
				// queued keeps its stale revision for authoritative rejection.
				job.values["revision"] = u.revision.Get()
			}
			if job.method == http.MethodPost {
				job.values["__cicada_location"] = workspaceLocation()
			}
			u.root.Call("setAttribute", "aria-busy", "true")
			u.status.Set("Saving…")
			projection, message, err := u.request(client, job)
			if u.closed.Load() {
				workspacePending(job, -1)
				return
			}
			if err == nil {
				err = u.apply(projection, job)
			}
			workspacePending(job, -1)
			u.root.Call("removeAttribute", "aria-busy")
			if err != nil {
				var rejected *workspaceRequestError
				if errors.As(err, &rejected) && (rejected.status == http.StatusBadRequest || rejected.status == http.StatusUnprocessableEntity) {
					// Validation has not mutated the score. Keep the draft editable
					// and let a corrected submission use the same revision.
					u.status.Set(err.Error())
					continue
				}
				// A failed/ambiguous mutation is never automatically submitted a
				// second time. Re-read canonical state before another edit.
				u.blocked.Store(true)
				u.clearQueue()
				u.status.Set(err.Error() + " Refresh the workspace; your drafts are retained.")
				continue
			}
			if projection.WriteRevision == "" && projection.Revision != u.revision.Get() {
				// A read can discover somebody else's edit. Queued gestures
				// based on the old projection must not be rebased over it.
				u.clearQueue()
			}
			u.revision.Set(projection.Revision)
			confirmedWorkspaceRevision.Set(projection.Revision)
			u.focusGesture(job)
			if job.refresh {
				u.blocked.Store(false)
			}
			if message == "" {
				message = "Saved."
				if job.method == http.MethodGet {
					message = "Workspace updated."
				}
			}
			u.status.Set(message)
		}
	}
}

type workspaceRequestError struct {
	status  int
	message string
}

func (err *workspaceRequestError) Error() string { return err.message }

func (u *workspaceUI) request(client *http.Client, job workspaceJob) (workspaceProjection, string, error) {
	var projection workspaceProjection
	var body io.Reader
	if job.method == http.MethodPost {
		payload, err := json.Marshal(job.values)
		if err != nil {
			return projection, "", err
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(u.ctx, job.method, js.Global().Get("location").Get("origin").String()+job.target, body)
	if err != nil {
		return projection, "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Cicada-Reactive", "1")
	if job.method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", u.props.CSRF)
	}
	response, err := client.Do(request)
	if err != nil {
		return projection, "", fmt.Errorf("The workspace request did not complete")
	}
	defer response.Body.Close()
	if job.method == http.MethodGet {
		if response.StatusCode != http.StatusOK {
			return projection, "", fmt.Errorf("Workspace refresh failed (%d)", response.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 12<<20)).Decode(&projection)
		return projection, "", err
	}
	var result struct {
		OK          bool                `json:"ok"`
		Message     string              `json:"message"`
		FieldErrors map[string]string   `json:"fieldErrors"`
		Data        workspaceProjection `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 12<<20)).Decode(&result); err != nil {
		return projection, "", fmt.Errorf("The server returned an incomplete workspace response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !result.OK {
		if result.Message == "" {
			result.Message = fmt.Sprintf("Edit failed (%d)", response.StatusCode)
		}
		for name, message := range result.FieldErrors {
			result.Message += " " + name + ": " + message
		}
		return projection, "", &workspaceRequestError{response.StatusCode, result.Message}
	}
	if result.Data.RefreshRequired || (result.Data.WriteRevision != "" && result.Data.WriteRevision != result.Data.Revision && result.Data.WriteRevision != result.Data.FileRevision) {
		if result.Message == "" {
			result.Message = "Saved. The score changed before the workspace refreshed."
		}
		return projection, "", fmt.Errorf("%s", result.Message)
	}
	return result.Data, result.Message, nil
}

type workspaceScroll struct {
	node      js.Value
	top, left float64
}

type workspaceEditorUpdate struct {
	node       js.Value
	key, raw   string
	value      string
	start, end int
	direction  string
	changed    bool
}

// The first-party editor normalizes whitespace-only LF lines on initial mount.
// Recognize exactly that rendering change; any other buffer difference remains
// a genuine draft. This mirrors native-editor.js's whitespace-only-line rule.
func workspaceEditorNormalized(raw string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.Trim(line, " \t") == "" {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func (u *workspaceUI) seedEditorBaseline() {
	editor := u.root.Call("querySelector", "textarea#editor-content")
	if !workspacePresent(editor) {
		return
	}
	raw, rendered := workspaceString(editor, "defaultValue"), workspaceString(editor, "value")
	if rendered == raw || rendered == workspaceEditorNormalized(raw) {
		u.editors[workspaceDraftKey(editor)] = workspaceEditorBaseline{raw, rendered}
		editor.Set("defaultValue", rendered)
	}
}

func (u *workspaceUI) scrolled(event js.Value) {
	node := event.Get("target")
	if !u.owned(node) {
		return
	}
	for _, scroll := range u.scrolls {
		if scroll.node.Equal(node) {
			return
		}
	}
	u.scrolls = append(u.scrolls, workspaceScroll{node: node})
}

func (u *workspaceUI) captureScrolls() []workspaceScroll {
	retained := u.scrolls[:0]
	for _, scroll := range u.scrolls {
		if scroll.node.Get("isConnected").Bool() {
			scroll.top, scroll.left = scroll.node.Get("scrollTop").Float(), scroll.node.Get("scrollLeft").Float()
			retained = append(retained, scroll)
		}
	}
	u.scrolls = retained
	return append([]workspaceScroll(nil), retained...)
}

func workspaceReadDraft(control js.Value) workspaceDraft {
	draft := workspaceDraft{value: workspaceString(control, "value")}
	if checked := control.Get("checked"); checked.Type() == js.TypeBoolean {
		value := checked.Bool()
		draft.checked = &value
	}
	if strings.EqualFold(workspaceString(control, "tagName"), "select") {
		draft.selected = map[string]bool{}
		options := control.Get("options")
		for i := 0; i < options.Length(); i++ {
			option := options.Index(i)
			draft.selected[workspaceString(option, "value")] = option.Get("selected").Bool()
		}
	}
	return draft
}

func workspaceParseHTML(raw string) (*html.Node, error) {
	document, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var body *html.Node
	var findBody func(*html.Node)
	findBody = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "body" {
			body = node
			return
		}
		for child := node.FirstChild; child != nil && body == nil; child = child.NextSibling {
			findBody(child)
		}
	}
	findBody(document)
	if body != nil {
		for child := body.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == html.ElementNode {
				return child, nil
			}
		}
	}
	return nil, fmt.Errorf("The server returned an invalid workspace root")
}

func workspaceHTMLAttr(node *html.Node, name string) (string, bool) {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val, true
		}
	}
	return "", false
}

func workspaceHTMLSetAttr(node *html.Node, name, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == name {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: name, Val: value})
}

func workspaceHTMLBool(node *html.Node, name string, value bool) {
	if value {
		workspaceHTMLSetAttr(node, name, "")
		return
	}
	for i, attr := range node.Attr {
		if attr.Key == name {
			node.Attr = append(node.Attr[:i], node.Attr[i+1:]...)
			return
		}
	}
}

func workspaceHTMLWalk(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		workspaceHTMLWalk(child, visit)
	}
}

func workspaceHTMLIdentity(node *html.Node) string {
	if node.Type != html.ElementNode {
		return ""
	}
	key := ""
	for _, name := range []string{"data-gosx-key", "id", "key"} {
		if value, _ := workspaceHTMLAttr(node, name); value != "" {
			prefix := "key:"
			if name == "id" {
				prefix = "id:"
			}
			key = prefix + value
			break
		}
	}
	if node.Data == "form" {
		action, _ := workspaceHTMLAttr(node, "action")
		key += "/form:" + action
		fields := map[string]string{}
		workspaceHTMLWalk(node, func(child *html.Node) {
			if child.Type == html.ElementNode && child.Data == "input" {
				kind, _ := workspaceHTMLAttr(child, "type")
				if kind == "hidden" {
					name, _ := workspaceHTMLAttr(child, "name")
					if _, exists := fields[name]; !exists {
						fields[name], _ = workspaceHTMLAttr(child, "value")
					}
				}
			}
		})
		for _, name := range []string{"action", "pattern", "lane", "step", "track", "scene", "slot", "newName", "path", "index"} {
			if value, exists := fields[name]; exists {
				key += "/" + name + ":" + value
			}
		}
		return key
	}
	if key != "" {
		return key
	}
	if name, _ := workspaceHTMLAttr(node, "name"); name != "" {
		key = "name:" + strings.ToUpper(node.Data) + ":" + name
		kind, _ := workspaceHTMLAttr(node, "type")
		if node.Data == "button" || kind == "radio" {
			value, _ := workspaceHTMLAttr(node, "value")
			key += "/value:" + value
		}
		return key
	}
	return ""
}

func workspaceHTMLDraftKey(node *html.Node) string {
	name, _ := workspaceHTMLAttr(node, "name")
	kind, _ := workspaceHTMLAttr(node, "type")
	if name == "" || kind == "hidden" || kind == "file" {
		return ""
	}
	for form := node.Parent; form != nil; form = form.Parent {
		if form.Type == html.ElementNode && form.Data == "form" {
			key := workspaceHTMLIdentity(form) + "/control:" + name
			if kind == "radio" {
				value, _ := workspaceHTMLAttr(node, "value")
				key += "/" + value
			}
			return key
		}
	}
	return ""
}

func workspaceHTMLControls(root *html.Node) map[string]*html.Node {
	controls := map[string]*html.Node{}
	workspaceHTMLWalk(root, func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "input" || node.Data == "textarea" || node.Data == "select") {
			if key := workspaceHTMLDraftKey(node); key != "" {
				controls[key] = node
			}
		}
	})
	return controls
}

func workspaceHTMLText(node *html.Node) string {
	var text strings.Builder
	workspaceHTMLWalk(node, func(child *html.Node) {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	})
	return text.String()
}

func workspaceHTMLOptionValue(node *html.Node) string {
	if value, exists := workspaceHTMLAttr(node, "value"); exists {
		return value
	}
	return workspaceHTMLText(node)
}

func workspaceHTMLValue(node *html.Node) string {
	if value, exists := workspaceHTMLAttr(node, "value"); exists {
		return value
	}
	if node.Data == "input" {
		kind, _ := workspaceHTMLAttr(node, "type")
		if kind == "checkbox" || kind == "radio" {
			return "on"
		}
	}
	if node.Data == "textarea" {
		return workspaceHTMLText(node)
	}
	if node.Data == "select" {
		first, selected := "", false
		workspaceHTMLWalk(node, func(option *html.Node) {
			if option.Type == html.ElementNode && option.Data == "option" {
				if !selected {
					first = workspaceHTMLOptionValue(option)
					selected = true
				}
				if _, exists := workspaceHTMLAttr(option, "selected"); exists {
					first = workspaceHTMLOptionValue(option)
				}
			}
		})
		return first
	}
	return ""
}

func workspaceHTMLDefaults(root *html.Node) map[string]workspaceDraft {
	defaults := map[string]workspaceDraft{}
	for key, control := range workspaceHTMLControls(root) {
		draft := workspaceDraft{value: workspaceHTMLValue(control)}
		if control.Data == "input" {
			_, checked := workspaceHTMLAttr(control, "checked")
			draft.checked = &checked
		}
		if control.Data == "select" {
			draft.selected = map[string]bool{}
			var first *html.Node
			anySelected := false
			workspaceHTMLWalk(control, func(option *html.Node) {
				if option.Type == html.ElementNode && option.Data == "option" {
					if first == nil {
						first = option
					}
					_, selected := workspaceHTMLAttr(option, "selected")
					anySelected = anySelected || selected
					draft.selected[workspaceHTMLOptionValue(option)] = selected
				}
			})
			_, multiple := workspaceHTMLAttr(control, "multiple")
			sizeText, _ := workspaceHTMLAttr(control, "size")
			size, _ := strconv.Atoi(sizeText)
			if !anySelected && !multiple && size <= 1 && first != nil {
				workspaceHTMLBool(first, "selected", true)
				draft.selected[workspaceHTMLOptionValue(first)] = true
			}
		}
		defaults[key] = draft
	}
	return defaults
}

func workspaceHTMLSetControl(node *html.Node, draft workspaceDraft) {
	workspaceHTMLSetAttr(node, "value", draft.value)
	if draft.checked != nil {
		workspaceHTMLBool(node, "checked", *draft.checked)
	}
	if node.Data == "select" {
		workspaceHTMLWalk(node, func(option *html.Node) {
			if option.Type == html.ElementNode && option.Data == "option" {
				workspaceHTMLBool(option, "selected", draft.selected[workspaceHTMLOptionValue(option)])
			}
		})
	}
}

func workspaceHTMLClone(node *html.Node) *html.Node {
	clone := &html.Node{Type: node.Type, DataAtom: node.DataAtom, Data: node.Data, Namespace: node.Namespace, Attr: append([]html.Attribute(nil), node.Attr...)}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		clone.AppendChild(workspaceHTMLClone(child))
	}
	return clone
}

func workspaceHTMLOpaque(node *html.Node) (bool, string) {
	engine, _ := workspaceHTMLAttr(node, "data-gosx-engine")
	island, hasIsland := workspaceHTMLAttr(node, "data-gosx-island")
	surface, hasSurface := workspaceHTMLAttr(node, "data-gosx-runtime-surface")
	_, hasEngine := workspaceHTMLAttr(node, "data-gosx-engine")
	opaque := (hasEngine || hasIsland || hasSurface || node.Data == "canvas" || node.Data == "audio" || node.Data == "video" || node.Data == "iframe") && engine != "CicadaTakes"
	return opaque, engine + "/" + island + "/" + surface
}

func workspaceHTMLPreserve(current, next *html.Node) {
	if current.Type != html.ElementNode || next.Type != html.ElementNode || current.Data != next.Data {
		return
	}
	// The native SSR toolbar is hidden until its engine mounts. Reintroducing
	// that fallback state during each canonical patch briefly shrinks the page
	// and can trigger scroll anchoring or viewport clamping before it reopens.
	if _, tools := workspaceHTMLAttr(current, "data-workspace-tools"); tools {
		if _, nextTools := workspaceHTMLAttr(next, "data-workspace-tools"); nextTools {
			_, hidden := workspaceHTMLAttr(current, "hidden")
			workspaceHTMLBool(next, "hidden", hidden)
		}
	}
	if tool, exists := workspaceHTMLAttr(current, "data-workspace-tool"); exists {
		if nextTool, _ := workspaceHTMLAttr(next, "data-workspace-tool"); tool == nextTool {
			if pressed, exists := workspaceHTMLAttr(current, "aria-pressed"); exists {
				workspaceHTMLSetAttr(next, "aria-pressed", pressed)
			}
		}
	}
	if _, help := workspaceHTMLAttr(current, "data-workspace-help"); help {
		if _, nextHelp := workspaceHTMLAttr(next, "data-workspace-help"); nextHelp {
			for next.FirstChild != nil {
				next.RemoveChild(next.FirstChild)
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				next.AppendChild(workspaceHTMLClone(child))
			}
			return
		}
	}
	if opaque, kind := workspaceHTMLOpaque(current); opaque && workspaceHTMLIdentity(current) != "" && workspaceHTMLIdentity(current) == workspaceHTMLIdentity(next) {
		if nextOpaque, nextKind := workspaceHTMLOpaque(next); nextOpaque && nextKind == kind {
			if current.Data != "audio" && current.Data != "video" && current.Data != "iframe" {
				next.Attr = append([]html.Attribute(nil), current.Attr...)
			}
			for next.FirstChild != nil {
				next.RemoveChild(next.FirstChild)
			}
			for child := current.FirstChild; child != nil; child = child.NextSibling {
				next.AppendChild(workspaceHTMLClone(child))
			}
			return
		}
	}
	if pending, exists := workspaceHTMLAttr(current, "data-workspace-pending"); exists {
		workspaceHTMLSetAttr(next, "data-workspace-pending", pending)
	}
	if current.Data == "details" {
		_, open := workspaceHTMLAttr(current, "open")
		workspaceHTMLBool(next, "open", open)
	}
	oldChildren := []*html.Node{}
	byKey := map[string]*html.Node{}
	for child := current.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			oldChildren = append(oldChildren, child)
			if key := workspaceHTMLIdentity(child); key != "" {
				byKey[key] = child
			}
		}
	}
	index := 0
	for child := next.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		key := workspaceHTMLIdentity(child)
		old := byKey[key]
		if old == nil && key == "" && index < len(oldChildren) && workspaceHTMLIdentity(oldChildren[index]) == "" {
			old = oldChildren[index]
		}
		if old != nil {
			workspaceHTMLPreserve(old, child)
		}
		index++
	}
}

func workspaceHTMLTree(root *html.Node) *vm.ResolvedTree {
	tree := &vm.ResolvedTree{}
	var appendNode func(*html.Node, string) int
	appendNode = func(node *html.Node, path string) int {
		if node.Type != html.ElementNode && node.Type != html.TextNode {
			return -1
		}
		resolved := vm.ResolvedNode{Key: workspaceHTMLIdentity(node)}
		if resolved.Key == "" {
			resolved.Key = "position:" + path
		}
		if node.Type == html.TextNode {
			resolved.Text = node.Data
		} else {
			resolved.Tag = node.Data
			for _, attr := range node.Attr {
				if attr.Key != "value" && attr.Key != "checked" && attr.Key != "selected" {
					name := attr.Key
					if attr.Namespace != "" {
						name = attr.Namespace + ":" + name
					}
					resolved.Attrs = append(resolved.Attrs, vm.ResolvedAttr{Name: name, Value: attr.Val})
				}
			}
			if node.Data == "input" || node.Data == "select" || node.Data == "textarea" {
				if kind, _ := workspaceHTMLAttr(node, "type"); kind != "file" {
					resolved.Attrs = append(resolved.Attrs, vm.ResolvedAttr{Name: "value", Value: workspaceHTMLValue(node)})
				}
			} else if value, exists := workspaceHTMLAttr(node, "value"); exists {
				resolved.Attrs = append(resolved.Attrs, vm.ResolvedAttr{Name: "value", Value: value})
			}
			for _, name := range []string{"checked", "selected"} {
				if _, exists := workspaceHTMLAttr(node, name); exists {
					resolved.Attrs = append(resolved.Attrs, vm.ResolvedAttr{Name: name, Bool: true})
				}
			}
		}
		index := len(tree.Nodes)
		tree.Nodes = append(tree.Nodes, resolved)
		position := 0
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			childIndex := appendNode(child, fmt.Sprintf("%s/%d", path, position))
			if childIndex >= 0 {
				tree.Nodes[index].Children = append(tree.Nodes[index].Children, childIndex)
			}
			position++
		}
		return index
	}
	appendNode(root, "root")
	return tree
}

func (u *workspaceUI) apply(projection workspaceProjection, job workspaceJob) (err error) {
	started := time.Now()
	measurements := map[string]float64{}
	phase := func(name string) {
		measurements[name] = float64(time.Since(started).Microseconds()) / 1000
		started = time.Now()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("The workspace view could not be updated")
		}
	}()
	if projection.HTML == "" || projection.Revision == "" {
		return fmt.Errorf("The server returned no canonical workspace")
	}
	document := js.Global().Get("document")
	current := u.root.Get("firstElementChild")
	if !workspacePresent(current) {
		return fmt.Errorf("The server returned an invalid workspace root")
	}
	// Browser-native serialization is one boundary crossing. Parse and walk the
	// snapshots in Go so a large piano roll does not require thousands of DOM
	// property reads for every queued note.
	previousHTML, err := workspaceParseHTML(workspaceString(current, "outerHTML"))
	if err != nil {
		return err
	}
	nextHTML, err := workspaceParseHTML(projection.HTML)
	if err != nil || previousHTML.Data != nextHTML.Data {
		return fmt.Errorf("The server returned an invalid workspace root")
	}
	phase("parse")
	scrolls := u.captureScrolls()
	x, y := js.Global().Get("scrollX").Float(), js.Global().Get("scrollY").Float()
	defaults := workspaceHTMLDefaults(nextHTML)
	var accepted []js.Value
	for _, control := range job.controls {
		if control.node.Get("isConnected").Bool() && control.signature == workspaceControlSignature(control.node) {
			accepted = append(accepted, control.node)
		}
	}
	controls := current.Call("querySelectorAll", workspaceEditable)
	previousControls := workspaceHTMLControls(previousHTML)
	keep := map[string]workspaceDraft{}
	canonicalEditors := map[string]workspaceDraft{}
	var editorUpdates []workspaceEditorUpdate
	for i := 0; i < controls.Length(); i++ {
		control := controls.Index(i)
		key := workspaceDraftKey(control)
		canonical, canonicalExists := defaults[key]
		canonicalRaw := canonical.value
		if workspaceAttr(control, "id") == "editor-content" && canonicalExists {
			if baseline, exists := u.editors[key]; exists && baseline.raw == canonicalRaw {
				canonical.value = baseline.rendered
				defaults[key] = canonical
			}
		}
		draft := workspaceReadDraft(control)
		if node := previousControls[key]; node != nil {
			workspaceHTMLSetControl(node, draft)
		}
		if workspaceKeepControl(control, job) {
			keep[key] = draft
		} else if workspaceAttr(control, "id") == "editor-content" {
			if canonicalExists {
				canonicalEditors[key] = canonical
				editorUpdates = append(editorUpdates, workspaceEditorUpdate{node: control, key: key, raw: canonicalRaw, value: canonical.value, start: control.Get("selectionStart").Int(), end: control.Get("selectionEnd").Int(), direction: workspaceString(control, "selectionDirection"), changed: draft.value != canonical.value})
			}
		}
	}
	u.cacheDrafts(current, job)
	workspaceHTMLPreserve(previousHTML, nextHTML)
	nextControls := workspaceHTMLControls(nextHTML)
	for key, draft := range keep {
		if node := nextControls[key]; node != nil {
			workspaceHTMLSetControl(node, draft)
		}
	}
	for key, draft := range u.drafts {
		if node := nextControls[key]; node != nil {
			workspaceHTMLSetControl(node, draft)
		}
	}
	// The first-party editor keeps its mounted DOM/listeners. A clean or
	// acknowledged buffer still follows canonical Undo, Redo and Format text.
	for key, canonical := range canonicalEditors {
		if node := nextControls[key]; node != nil {
			workspaceHTMLSetControl(node, canonical)
		}
	}
	phase("state")
	previousTree, nextTree := workspaceHTMLTree(previousHTML), workspaceHTMLTree(nextHTML)
	ops := vm.ReconcileTrees(previousTree, nextTree, nil)
	encoded, err := workspaceEncodePatches(ops)
	if err != nil {
		return err
	}
	phase("diff")
	u.patch.Invoke("cicada-workspace", string(encoded))
	phase("patch")
	for _, control := range accepted {
		if control.Get("isConnected").Bool() {
			workspaceAcceptControl(control)
		}
	}
	workspaceSetDefaults(u.root.Get("firstElementChild"), defaults)
	for _, editor := range editorUpdates {
		if !editor.node.Get("isConnected").Bool() {
			continue
		}
		if editor.changed {
			if workspaceString(editor.node, "value") != editor.value {
				editor.node.Set("value", editor.value)
			}
			editor.node.Call("setSelectionRange", editor.start, editor.end, editor.direction)
			// Let the editor's own renderer normalize and rehighlight its buffer.
			// This is synchronous; a real in-flight draft was excluded above.
			editor.node.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		}
		// Keep raw source separate from the editor's rendered clean baseline.
		// Reapplying the same canonical source must not create a false draft or
		// rewrite a normalized buffer. Unchanged saves preserve the exact caret.
		rendered := workspaceString(editor.node, "value")
		editor.node.Set("defaultValue", rendered)
		u.editors[editor.key] = workspaceEditorBaseline{editor.raw, rendered}
	}
	u.syncTools()
	// Owned editor/live DOM is retained, including its nested forms. A
	// confirmed projection still advances those forms' concurrency token.
	revisions := u.root.Call("querySelectorAll", "input[type=hidden][name=revision]")
	for i := 0; i < revisions.Length(); i++ {
		control := revisions.Index(i)
		control.Set("value", projection.Revision)
		control.Set("defaultValue", projection.Revision)
	}
	if projection.FileRevision != "" {
		fields := u.root.Call("querySelectorAll", "input[type=hidden][name=fileRevision],input[type=hidden][name=diskRevision]")
		for i := 0; i < fields.Length(); i++ {
			control := fields.Index(i)
			if workspaceAttr(control, "name") == "fileRevision" {
				editor := u.root.Call("querySelector", "textarea")
				if workspacePresent(editor) && workspaceDirty(editor) {
					continue
				}
			}
			control.Set("value", projection.FileRevision)
			control.Set("defaultValue", projection.FileRevision)
		}
	}
	for _, scroll := range scrolls {
		if scroll.node.Get("isConnected").Bool() {
			scroll.node.Set("scrollTop", scroll.top)
			scroll.node.Set("scrollLeft", scroll.left)
		}
	}
	js.Global().Call("scrollTo", x, y)
	if projection.Title != "" {
		document.Set("title", projection.Title)
	}
	if projection.Location != "" {
		if location, safe := workspaceURL(projection.Location); safe {
			js.Global().Get("history").Call("replaceState", js.Null(), "", location.RequestURI()+workspaceFragment(location))
		}
	}
	phase("finish")
	if location, _ := url.Parse(workspaceLocation()); location.Query().Get("__workspace_profile") == "1" || workspaceAttr(u.root, "data-workspace-profile") == "true" {
		measurements["nodes"] = float64(len(nextTree.Nodes))
		measurements["ops"] = float64(len(ops))
		encoded, _ := json.Marshal(measurements)
		u.root.Call("setAttribute", "data-workspace-timing", string(encoded))
	}
	return nil
}

// GoSX's SetValue operation deliberately updates the live value property. A
// server projection also supplies the new reset/dirty baseline, including for
// controls that the patch receiver just created. Keep the live draft while
// applying that baseline, so later unrelated changes do not mistake a clean
// newly-created control for an unsaved edit.
func workspaceSetDefaults(root js.Value, defaults map[string]workspaceDraft) {
	controls := root.Call("querySelectorAll", workspaceEditable)
	for i := 0; i < controls.Length(); i++ {
		control := controls.Index(i)
		baseline, exists := defaults[workspaceDraftKey(control)]
		if !exists {
			continue
		}
		tag := strings.ToLower(workspaceString(control, "tagName"))
		value := workspaceString(control, "value")
		if tag == "input" || tag == "textarea" {
			control.Set("defaultValue", baseline.value)
			if workspaceString(control, "value") != value {
				control.Set("value", value)
			}
		}
		if baseline.checked != nil {
			checked := control.Get("checked").Bool()
			control.Set("defaultChecked", *baseline.checked)
			control.Set("checked", checked)
		}
		if tag == "select" {
			options := control.Get("options")
			selected := make([]bool, options.Length())
			for j := 0; j < options.Length(); j++ {
				selected[j] = options.Index(j).Get("selected").Bool()
			}
			for j := 0; j < options.Length(); j++ {
				option := options.Index(j)
				option.Set("defaultSelected", baseline.selected[workspaceString(option, "value")])
			}
			for j, value := range selected {
				options.Index(j).Set("selected", value)
			}
		}
	}
}

func workspaceAcceptControl(node js.Value) {
	tag := strings.ToLower(workspaceString(node, "tagName"))
	if (tag == "input" || tag == "textarea") && workspaceString(node, "type") != "file" {
		node.Set("defaultValue", workspaceString(node, "value"))
	}
	if value := node.Get("checked"); value.Type() == js.TypeBoolean {
		node.Set("defaultChecked", value.Bool())
	}
	if tag == "select" {
		options := node.Get("options")
		for i := 0; i < options.Length(); i++ {
			option := options.Index(i)
			option.Set("defaultSelected", option.Get("selected").Bool())
		}
	}
}

func workspaceFragment(location *url.URL) string {
	if location.Fragment == "" {
		return ""
	}
	return "#" + location.EscapedFragment()
}

func workspaceControls(form js.Value) []workspaceControl {
	nodes := form.Call("querySelectorAll", "input, select, textarea")
	out := make([]workspaceControl, 0, nodes.Length())
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		out = append(out, workspaceControl{node, workspaceControlSignature(node)})
	}
	return out
}

func workspaceControlSignature(node js.Value) string {
	signature := workspaceString(node, "value")
	if checked := node.Get("checked"); checked.Type() == js.TypeBoolean {
		signature += fmt.Sprintf("/%t", checked.Bool())
	}
	if strings.EqualFold(workspaceString(node, "tagName"), "select") {
		options := node.Get("options")
		for i := 0; i < options.Length(); i++ {
			signature += fmt.Sprintf("/%t", options.Index(i).Get("selected").Bool())
		}
	}
	return signature
}

func workspaceDirty(node js.Value) bool {
	tag := strings.ToLower(workspaceString(node, "tagName"))
	if tag == "input" {
		kind := strings.ToLower(workspaceString(node, "type"))
		if kind == "hidden" {
			return false
		}
		if kind == "checkbox" || kind == "radio" {
			return node.Get("checked").Bool() != node.Get("defaultChecked").Bool()
		}
		return workspaceString(node, "value") != workspaceString(node, "defaultValue")
	}
	if tag == "textarea" {
		return workspaceString(node, "value") != workspaceString(node, "defaultValue")
	}
	if tag == "select" {
		options := node.Get("options")
		for i := 0; i < options.Length(); i++ {
			option := options.Index(i)
			if option.Get("selected").Bool() != option.Get("defaultSelected").Bool() {
				return true
			}
		}
	}
	return false
}

func workspaceKeepControl(node js.Value, job workspaceJob) bool {
	if workspacePresent(job.form) && job.form.Call("contains", node).Bool() {
		for _, control := range job.controls {
			if control.node.Equal(node) {
				return control.signature != workspaceControlSignature(node)
			}
		}
	}
	return workspaceDirty(node)
}

func workspaceIdentity(node js.Value) string {
	if node.Get("nodeType").Int() != 1 {
		return ""
	}
	key := ""
	if value := workspaceAttr(node, "data-gosx-key"); value != "" {
		key = "key:" + value
	} else if value := workspaceAttr(node, "id"); value != "" {
		key = "id:" + value
	} else if value := workspaceAttr(node, "key"); value != "" {
		key = "key:" + value
	}
	if strings.EqualFold(workspaceString(node, "tagName"), "form") {
		// A step inspector is a different semantic form when its hidden step,
		// lane or pattern changes, even when its visual container keeps an ID.
		key += "/form:" + workspaceAttr(node, "action")
		for _, name := range []string{"action", "pattern", "lane", "step", "track", "scene", "slot", "newName", "path", "index"} {
			field := node.Call("querySelector", "input[type=hidden][name="+name+"]")
			if workspacePresent(field) {
				key += "/" + name + ":" + workspaceString(field, "value")
			}
		}
		return key
	}
	if key != "" {
		return key
	}
	if name := workspaceAttr(node, "name"); name != "" {
		key := "name:" + workspaceString(node, "tagName") + ":" + name
		if strings.EqualFold(workspaceString(node, "tagName"), "button") || workspaceString(node, "type") == "radio" {
			key += "/value:" + workspaceString(node, "value")
		}
		return key
	}
	return ""
}

func workspaceDraftKey(control js.Value) string {
	name := workspaceAttr(control, "name")
	if name == "" || workspaceString(control, "type") == "hidden" || workspaceString(control, "type") == "file" {
		return ""
	}
	form := control.Get("form")
	if !workspacePresent(form) {
		return ""
	}
	key := workspaceIdentity(form) + "/control:" + name
	if kind := workspaceString(control, "type"); kind == "radio" {
		key += "/" + workspaceString(control, "value")
	}
	return key
}

func (u *workspaceUI) cacheDrafts(root js.Value, job workspaceJob) {
	controls := root.Call("querySelectorAll", workspaceEditable)
	for i := 0; i < controls.Length(); i++ {
		control := controls.Index(i)
		key := workspaceDraftKey(control)
		if key == "" {
			continue
		}
		accepted := false
		for _, submitted := range job.controls {
			if submitted.node.Equal(control) && submitted.signature == workspaceControlSignature(control) {
				accepted = true
				break
			}
		}
		if accepted || !workspaceDirty(control) {
			delete(u.drafts, key)
			continue
		}
		draft := workspaceDraft{value: workspaceString(control, "value")}
		if checked := control.Get("checked"); checked.Type() == js.TypeBoolean {
			value := checked.Bool()
			draft.checked = &value
		}
		if strings.EqualFold(workspaceString(control, "tagName"), "select") {
			draft.selected = map[string]bool{}
			options := control.Get("options")
			for j := 0; j < options.Length(); j++ {
				option := options.Index(j)
				draft.selected[workspaceString(option, "value")] = option.Get("selected").Bool()
			}
		}
		if _, exists := u.drafts[key]; !exists && len(u.drafts) >= 512 {
			for old := range u.drafts {
				delete(u.drafts, old)
				break
			}
		}
		u.drafts[key] = draft
	}
}

func workspaceScrolls(root js.Value) []workspaceScroll {
	nodes := root.Call("querySelectorAll", "*")
	out := []workspaceScroll{{root, root.Get("scrollTop").Float(), root.Get("scrollLeft").Float()}}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		top, left := node.Get("scrollTop").Float(), node.Get("scrollLeft").Float()
		if top != 0 || left != 0 {
			out = append(out, workspaceScroll{node, top, left})
		}
	}
	return out
}

// Poll a compact content fingerprint, then reuse the workspace patch path.
// It refreshes clean editors and projections while retaining unsaved drafts.
func (u *workspaceUI) watchFiles() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	seen := u.props.ProjectRevision
	client := &http.Client{Timeout: 3 * time.Second}
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
		req, err := http.NewRequestWithContext(u.ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/revision", nil)
		if err != nil {
			return
		}
		response, err := client.Do(req)
		if err != nil {
			continue
		}
		var state struct {
			Revision string `json:"projectRevision"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&state)
		response.Body.Close()
		if err != nil || state.Revision == "" || state.Revision == seen {
			continue
		}
		seen = state.Revision
		if u.blocked.Load() {
			continue
		}
		location, _ := url.Parse(workspaceLocation())
		u.queue(workspaceJob{method: http.MethodGet, target: "/__workspace?" + location.RawQuery, form: js.Undefined()})
	}
}
