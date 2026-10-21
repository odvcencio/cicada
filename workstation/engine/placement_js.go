//go:build js && wasm

package main

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"syscall/js"

	"m31labs.dev/cicada/kernel/seq"
)

type placementDrag struct {
	block, scroller  js.Value
	pointer          int
	x, scroll, scale float64
	at, length       int64
	next             int64
	resize, moved    bool
	left, width      string
}

// placementEditorUI shares move/trim geometry between the panel application
// and Studio. Persistence remains in each surface's ordinary guarded actions.
type placementEditorUI struct {
	drag            *placementDrag
	dragClick       bool
	owned           func(js.Value) bool
	blocked         func() bool
	status          func(string)
	selectPlacement func(js.Value)
	placementEdit   func(js.Value, string, int64)
	refresh         func(js.Value)
}

func placementTicks(block js.Value, name string) int64 {
	value, _ := strconv.ParseInt(workspaceAttr(block, name), 10, 64)
	return value
}

func selectPlacement(root, block js.Value) {
	id := workspaceAttr(block, "data-placement-block")
	location, _ := url.Parse(workspaceLocation())
	query := location.Query()
	query.Set("placement", id)
	location.RawQuery = query.Encode()
	js.Global().Get("history").Call("replaceState", js.Null(), "", location.RequestURI())
	blocks := root.Call("querySelectorAll", "[data-placement-block]")
	for i := 0; i < blocks.Length(); i++ {
		node := blocks.Index(i)
		node.Call("setAttribute", "data-placement-selected", strconv.FormatBool(node.Equal(block)))
	}
	inspectors := root.Call("querySelectorAll", "[data-placement-inspector]")
	for i := 0; i < inspectors.Length(); i++ {
		node := inspectors.Index(i)
		node.Set("open", workspaceAttr(node, "data-placement-inspector") == id)
	}
}

func (u *placementEditorUI) placementPointerDown(event js.Value) {
	u.dragClick = false
	target := event.Get("target")
	if u.drag != nil || !u.owned(target) || target.Get("closest").Type() != js.TypeFunction || event.Get("button").Int() != 0 || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("altKey").Bool() || event.Get("shiftKey").Bool() {
		return
	}
	control := target.Call("closest", ".placement-select, [data-placement-resize]")
	if !workspacePresent(control) || workspaceNative(control) || workspaceEngineOwned(control) {
		return
	}
	if u.blocked() {
		workspaceStop(event)
		u.dragClick = true
		return
	}
	block := control.Call("closest", "[data-placement-block]")
	lane := block.Call("closest", "[data-placement-ticks]")
	scroller := block.Call("closest", ".placement-scroll")
	width := lane.Call("getBoundingClientRect").Get("width").Float()
	ticks := placementTicks(lane, "data-placement-ticks")
	if width <= 0 || ticks <= 0 {
		return
	}
	workspaceStop(event)
	u.selectPlacement(block)
	// A drag owns the viewport until the user enables Follow again.
	if nav := block.Call("closest", "[data-placement-navigation]"); workspacePresent(nav) {
		follow := nav.Call("querySelector", "[name=placementFollow]")
		if workspacePresent(follow) && follow.Get("checked").Bool() {
			follow.Set("checked", false)
			follow.Call("dispatchEvent", js.Global().Get("Event").New("change", map[string]any{"bubbles": true}))
		}
	}
	control.Call("focus", map[string]any{"preventScroll": true})
	at, length := placementTicks(block, "data-placement-at"), placementTicks(block, "data-placement-length")
	resize := control.Call("hasAttribute", "data-placement-resize").Bool()
	next := at
	if resize {
		next = length
	}
	u.drag = &placementDrag{block: block, scroller: scroller, pointer: event.Get("pointerId").Int(), x: event.Get("clientX").Float(), scroll: scroller.Get("scrollLeft").Float(), scale: width / float64(ticks), at: at, length: length, next: next, resize: resize, left: block.Get("style").Get("left").String(), width: block.Get("style").Get("width").String()}
	block.Call("setPointerCapture", u.drag.pointer)
}

func (u *placementEditorUI) placementPointerMove(event js.Value) {
	drag := u.drag
	if drag == nil || event.Get("pointerId").Int() != drag.pointer {
		return
	}
	workspaceStop(event)
	dx := event.Get("clientX").Float() - drag.x + drag.scroller.Get("scrollLeft").Float() - drag.scroll
	if !drag.moved && math.Abs(dx) < 4 {
		return
	}
	drag.moved = true
	delta := int64(math.Round(dx/drag.scale/float64(seq.TicksPerStep))) * seq.TicksPerStep
	field, next := "at", max(int64(0), drag.at+delta)
	if drag.resize {
		field, next = "length", max(int64(1), drag.length+delta)
	}
	drag.next = next
	ticks := placementTicks(drag.block.Call("closest", "[data-placement-ticks]"), "data-placement-ticks")
	style := drag.block.Get("style")
	if drag.resize {
		style.Set("width", strconv.FormatFloat(float64(next)*100/float64(ticks), 'f', 6, 64)+"%")
	} else {
		style.Set("left", strconv.FormatFloat(float64(next)*100/float64(ticks), 'f', 6, 64)+"%")
	}
	drag.block.Call("setAttribute", "data-placement-preview", field)
	value := strconv.FormatInt(next, 10) + "ticks"
	if !drag.resize && next%seq.TicksPerStep == 0 {
		value = fmt.Sprintf("@%d.%d.%d", next/seq.TicksPerBar+1, next%seq.TicksPerBar/seq.PPQ+1, next%seq.PPQ/seq.TicksPerStep+1)
	}
	u.status(workspaceAttr(drag.block, "data-placement-block") + " · " + field + " " + value)
}

func (u *placementEditorUI) clearPlacementDrag() *placementDrag {
	drag := u.drag
	if drag == nil {
		return nil
	}
	u.drag = nil
	drag.block.Get("style").Set("left", drag.left)
	drag.block.Get("style").Set("width", drag.width)
	drag.block.Call("removeAttribute", "data-placement-preview")
	if drag.block.Call("hasPointerCapture", drag.pointer).Bool() {
		drag.block.Call("releasePointerCapture", drag.pointer)
	}
	return drag
}

func (u *placementEditorUI) placementPointerUp(event js.Value) {
	if u.drag == nil || event.Get("pointerId").Int() != u.drag.pointer {
		return
	}
	u.placementPointerMove(event)
	drag := u.clearPlacementDrag()
	field, before := "at", drag.at
	if drag.resize {
		field, before = "length", drag.length
	}
	if drag.next != before {
		workspaceStop(event)
		u.dragClick = true
		u.placementEdit(drag.block, field, drag.next)
	} else {
		// Pointer capture targets the click at the block rather than its link.
		// Read the selected view so its defaults and revision also follow edits
		// saved from another panel or browser.
		u.refresh(drag.block)
	}
}

func (u *placementEditorUI) placementPointerCancel(event js.Value) {
	if u.drag != nil && event.Get("pointerId").Int() == u.drag.pointer {
		u.clearPlacementDrag()
		u.dragClick = true
		u.status("Placement edit cancelled.")
	}
}

func (u *placementEditorUI) placementKeyDown(event js.Value) bool {
	if u.drag != nil && event.Get("key").String() == "Escape" {
		workspaceStop(event)
		u.clearPlacementDrag()
		u.dragClick = true
		u.status("Placement edit cancelled.")
		return true
	}
	target := event.Get("target")
	if !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
		return false
	}
	control := target.Call("closest", ".placement-select, [data-placement-resize]")
	key := event.Get("key").String()
	if !u.owned(target) || !workspacePresent(control) || (key != "ArrowLeft" && key != "ArrowRight") || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("altKey").Bool() {
		return false
	}
	workspaceStop(event)
	if u.blocked() {
		return true
	}
	block := control.Call("closest", "[data-placement-block]")
	field, attr, minimum := "at", "data-placement-at", int64(0)
	if control.Call("hasAttribute", "data-placement-resize").Bool() {
		field, attr, minimum = "length", "data-placement-length", 1
	}
	delta := int64(seq.TicksPerStep)
	if event.Get("shiftKey").Bool() {
		delta = seq.TicksPerBar
	}
	if key == "ArrowLeft" {
		delta = -delta
	}
	before := placementTicks(block, attr)
	next := max(minimum, before+delta)
	if next != before {
		u.selectPlacement(block)
		u.placementEdit(block, field, next)
	}
	return true
}

func (u *workspaceUI) placementEdit(block js.Value, field string, value int64) {
	id := workspaceAttr(block, "data-placement-block")
	values := u.editValues()
	values["action"], values["name"], values[field] = "update", id, strconv.FormatInt(value, 10)+"ticks"
	var form js.Value = js.Undefined()
	var controls []workspaceControl
	inspectors := u.root.Call("querySelectorAll", "[data-placement-inspector]")
	for i := 0; i < inspectors.Length(); i++ {
		inspector := inspectors.Index(i)
		if workspaceAttr(inspector, "data-placement-inspector") == id {
			form = inspector.Call("querySelector", "form")
			control := workspaceField(form, field)
			if workspacePresent(control) {
				controls = append(controls, workspaceControl{control, workspaceControlSignature(control)})
			}
			break
		}
	}
	u.queue(workspaceJob{method: http.MethodPost, target: "/__actions/arrange", values: values, confirmed: u.revision.Get(), form: form, controls: controls, pendingCell: block})
}

func (u *placementEditorUI) placementViewChanged(event js.Value) {
	if workspaceAttr(event.Get("target"), "name") == "placementZoom" && u.clearPlacementDrag() != nil {
		u.dragClick = true
		u.status("Placement edit cancelled after zoom changed.")
	}
}
