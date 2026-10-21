//go:build js && wasm

package main

import (
	"net/url"
	"strconv"
	"syscall/js"
)

// Studio gestures submit the same forms as the placement inspector. GoSX owns
// pending/error state and managed returns; no second save queue or polling
// workspace engine is mounted in the shell.
func bindShellPlacementEditor(root js.Value) func() {
	document := js.Global().Get("document")
	status := func(message string) {
		node := root.Call("querySelector", "[data-placement-edit-status]")
		if workspacePresent(node) && node.Get("textContent").String() != message {
			node.Set("textContent", message)
		}
	}
	blocked := func() bool {
		reason := ""
		arrange := root.Call("querySelector", "#view-panel-arrange")
		switch {
		case !root.Get("isConnected").Bool(), !workspacePresent(arrange), arrange.Get("hidden").Bool():
			return true
		case nativeCapture.Get().State.Active != "":
			reason = "Finish recording before moving placements."
		case workspacePresent(root.Call("querySelector", "[data-gosx-form-state=pending]")):
			reason = "Saving changes. Wait before editing another placement."
		case hasUnsavedWork():
			reason = "Save or discard unsaved edits before moving placements."
		}
		if reason != "" {
			status(reason)
			return true
		}
		return false
	}
	// Both surfaces select by the same authored placement identity.
	u := &placementEditorUI{status: status, blocked: blocked, selectPlacement: func(block js.Value) { selectPlacement(root, block) }}
	u.owned = func(node js.Value) bool {
		return workspacePresent(node) && node.Get("closest").Type() == js.TypeFunction && root.Call("contains", node).Bool() && workspacePresent(node.Call("closest", "#view-panel-arrange"))
	}
	u.refresh = func(block js.Value) {
		link := block.Call("querySelector", ".placement-select")
		if workspacePresent(link) {
			link.Call("click")
		}
	}
	u.placementEdit = func(block js.Value, field string, value int64) {
		if blocked() {
			return
		}
		id := workspaceAttr(block, "data-placement-block")
		forms := root.Call("querySelectorAll", "[data-placement-inspector]")
		for i := 0; i < forms.Length(); i++ {
			if inspector := forms.Index(i); workspaceAttr(inspector, "data-placement-inspector") == id {
				form := inspector.Call("querySelector", "form")
				control := workspaceField(form, field)
				submit := form.Call("querySelector", "button[type=submit]")
				if !workspacePresent(control) || !workspacePresent(submit) {
					return
				}
				// A prior rejected form may retain its fields. Read untouched
				// values from the canonical block, not that rejected draft.
				for name, canonical := range map[string]string{
					"track":   workspaceAttr(block, "data-placement-track"),
					"content": workspaceAttr(block, "data-placement-content"),
					"at":      workspaceAttr(block, "data-placement-at") + "ticks",
					"length":  workspaceAttr(block, "data-placement-length") + "ticks",
				} {
					if input := workspaceField(form, name); workspacePresent(input) {
						input.Set("value", canonical)
					}
				}
				control.Set("value", strconv.FormatInt(value, 10)+"ticks")
				focusControl := ".placement-select"
				if active := document.Get("activeElement"); workspacePresent(active) && active.Call("hasAttribute", "data-placement-resize").Bool() {
					focusControl = "[data-placement-resize]"
				}
				js.Global().Set(placementFocusKey, map[string]any{"id": id, "control": focusControl, "field": "data-placement-" + field, "value": strconv.FormatInt(value, 10)})
				status("Saving " + id + "…")
				form.Call("requestSubmit", submit)
				return
			}
		}
	}
	var bindings []workspaceEvent
	listenTo := func(target js.Value, name string, fn func(js.Value)) {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if root.Get("isConnected").Bool() && len(args) > 0 {
				fn(args[0])
			}
			return nil
		})
		target.Call("addEventListener", name, callback, true)
		bindings = append(bindings, workspaceEvent{target, name, callback})
	}
	listen := func(name string, fn func(js.Value)) { listenTo(document, name, fn) }
	listen("pointerdown", func(event js.Value) {
		// A later pointer action belongs to the user, even while a save waits.
		js.Global().Set(placementFocusKey, js.Undefined())
		u.placementPointerDown(event)
	})
	listen("pointermove", u.placementPointerMove)
	listen("pointerup", u.placementPointerUp)
	listen("pointercancel", u.placementPointerCancel)
	listen("lostpointercapture", u.placementPointerCancel)
	listen("keydown", func(event js.Value) {
		if event.Get("key").String() == "Tab" {
			js.Global().Set(placementFocusKey, js.Undefined())
		}
		u.placementKeyDown(event)
	})
	listen("focusin", func(event js.Value) {
		pending := js.Global().Get(placementFocusKey)
		target := event.Get("target")
		if !workspacePresent(pending) || !workspacePresent(target) || workspaceAttr(target, "id") == "studio-center" {
			return
		}
		if target.Get("closest").Type() != js.TypeFunction || workspaceAttr(target.Call("closest", "[data-placement-block]"), "data-placement-block") != workspaceString(pending, "id") {
			js.Global().Set(placementFocusKey, js.Undefined())
		}
	})
	listen("gosx:form:result", func(event js.Value) {
		detail := event.Get("detail")
		if !workspacePresent(detail) {
			return
		}
		action, err := url.Parse(workspaceString(detail, "action"))
		if err == nil && action.Path == "/__actions/arrange" && detail.Get("ok").Type() == js.TypeBoolean && !detail.Get("ok").Bool() {
			js.Global().Set(placementFocusKey, js.Undefined())
		}
	})
	listen("click", func(event js.Value) {
		u.placementViewChanged(event)
		target := event.Get("target")
		if u.dragClick && workspacePresent(target) && target.Get("closest").Type() == js.TypeFunction && workspacePresent(target.Call("closest", "[data-placement-block]")) {
			u.dragClick = false
			workspaceStop(event)
		}
	})
	listen("change", u.placementViewChanged)
	listen("visibilitychange", u.placementViewportChanged)
	listenTo(js.Global(), "blur", u.placementViewportChanged)
	listenTo(js.Global(), "resize", u.placementViewportChanged)
	listen("gosx:navigate", func(js.Value) {
		u.clearPlacementDrag()
		restorePlacementFocus(root)
	})
	root.Call("setAttribute", "data-shell-placement-editor", "ready")
	restorePlacementFocus(root)
	return func() {
		u.clearPlacementDrag()
		for _, b := range bindings {
			b.target.Call("removeEventListener", b.name, b.callback, true)
			b.callback.Release()
		}
		root.Call("removeAttribute", "data-shell-placement-editor")
	}
}

// A managed save disposes this engine before it moves focus to MAIN, then
// mounts a new engine after gosx:navigate. Keep only a data descriptor on the
// window across that handoff. A hard reload clears it; later pointer/Tab intent,
// a rejected action, or the destination consumes it without retaining callbacks.
const placementFocusKey = "__cicadaPlacementFocus"

func restorePlacementFocus(root js.Value) {
	pending := js.Global().Get(placementFocusKey)
	if !workspacePresent(pending) {
		return
	}
	document := js.Global().Get("document")
	id := workspaceString(pending, "id")
	active := document.Get("activeElement")
	if workspacePresent(active) && !active.Equal(document.Get("body")) && workspaceAttr(active, "id") != "studio-center" {
		if active.Get("closest").Type() == js.TypeFunction && workspaceAttr(active.Call("closest", "[data-placement-block]"), "data-placement-block") == id {
			return // Still focused before managed navigation's MAIN handoff.
		}
		js.Global().Set(placementFocusKey, js.Undefined())
		return // The user chose another control while this save was pending.
	}
	js.Global().Set(placementFocusKey, js.Undefined())
	arrange := root.Call("querySelector", "#view-panel-arrange")
	if !workspacePresent(arrange) || arrange.Get("hidden").Bool() {
		return
	}
	blocks := arrange.Call("querySelectorAll", "[data-placement-block][data-placement-selected=true]")
	for i := 0; i < blocks.Length(); i++ {
		block := blocks.Index(i)
		if workspaceAttr(block, "data-placement-block") == id && workspaceAttr(block, workspaceString(pending, "field")) == workspaceString(pending, "value") {
			block.Call("querySelector", workspaceString(pending, "control")).Call("focus", map[string]any{"preventScroll": true})
			return
		}
	}
}
