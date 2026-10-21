//go:build js && wasm

package main

import (
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
				status("Saving " + id + "…")
				form.Call("requestSubmit", submit)
				return
			}
		}
	}
	var bindings []workspaceEvent
	listen := func(name string, fn func(js.Value)) {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if root.Get("isConnected").Bool() && len(args) > 0 {
				fn(args[0])
			}
			return nil
		})
		document.Call("addEventListener", name, callback, true)
		bindings = append(bindings, workspaceEvent{document, name, callback})
	}
	listen("pointerdown", u.placementPointerDown)
	listen("pointermove", u.placementPointerMove)
	listen("pointerup", u.placementPointerUp)
	listen("pointercancel", u.placementPointerCancel)
	listen("lostpointercapture", u.placementPointerCancel)
	listen("keydown", func(event js.Value) { u.placementKeyDown(event) })
	listen("click", func(event js.Value) {
		target := event.Get("target")
		if u.dragClick && workspacePresent(target) && target.Get("closest").Type() == js.TypeFunction && workspacePresent(target.Call("closest", "[data-placement-block]")) {
			u.dragClick = false
			workspaceStop(event)
		}
	})
	listen("change", u.placementViewChanged)
	listen("gosx:navigate", func(js.Value) { u.clearPlacementDrag() })
	root.Call("setAttribute", "data-shell-placement-editor", "ready")
	return func() {
		u.clearPlacementDrag()
		for _, b := range bindings {
			b.target.Call("removeEventListener", b.name, b.callback, true)
			b.callback.Release()
		}
		root.Call("removeAttribute", "data-shell-placement-editor")
	}
}
