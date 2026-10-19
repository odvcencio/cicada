//go:build js && wasm

package main

import (
	"context"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// Placement commands are available only in Arrange. Focused text editors and
// other engines retain their own keys, including Delete in the piano roll.
func selectedPlacementInspector(root js.Value) js.Value {
	if !workspacePresent(root) || !root.Get("isConnected").Bool() || nativeCapture.Get().State.Active != "" {
		return js.Undefined()
	}
	arrange := root.Call("querySelector", "#view-panel-arrange")
	if !workspacePresent(arrange) || arrange.Get("hidden").Bool() || hasUnsavedWork() {
		return js.Undefined()
	}
	active := js.Global().Get("document").Get("activeElement")
	if workspacePresent(active) && active.Get("closest").Type() == js.TypeFunction && workspacePresent(active.Call("closest", "input, textarea, select, [contenteditable]:not([contenteditable=false]), [role=textbox], .piano-roll, #studio-detail, #studio-browser, #studio-code, [role=dialog]")) {
		return js.Undefined()
	}
	blocks := arrange.Call("querySelectorAll", "[data-placement-block][data-placement-selected=true]")
	if blocks.Length() != 1 {
		return js.Undefined()
	}
	id := workspaceAttr(blocks.Index(0), "data-placement-block")
	inspectors := arrange.Call("querySelectorAll", "[data-placement-inspector]")
	for i := 0; i < inspectors.Length(); i++ {
		if inspector := inspectors.Index(i); workspaceAttr(inspector, "data-placement-inspector") == id {
			return inspector
		}
	}
	return js.Undefined()
}

// Capture-phase updates run before GoSX matches commands. The When guard then
// leaves unrelated keyboard behavior untouched, rather than consuming a key
// and discovering afterward that there was no eligible placement.
func watchPlacementCommands(host enginewasm.Context, root js.Value) func() {
	document := js.Global().Get("document")
	update := func() {
		_ = host.SetSignal("$placementSelectionActive", workspacePresent(selectedPlacementInspector(root)))
	}
	callback := js.FuncOf(func(js.Value, []js.Value) any { update(); return nil })
	for _, name := range []string{"focusin", "pointerdown", "keydown", "input", "change"} {
		document.Call("addEventListener", name, callback, true)
	}
	observer := js.Global().Get("MutationObserver").New(callback)
	if arrange := root.Call("querySelector", "#view-panel-arrange"); workspacePresent(arrange) {
		observer.Call("observe", arrange, map[string]any{"subtree": true, "attributes": true, "attributeFilter": []any{"hidden", "data-placement-selected"}})
	}
	update()
	return func() {
		observer.Call("disconnect")
		for _, name := range []string{"focusin", "pointerdown", "keydown", "input", "change"} {
			document.Call("removeEventListener", name, callback, true)
		}
		callback.Release()
	}
}

func submitPlacementCommand(ctx context.Context, root js.Value, id string) {
	inspector := selectedPlacementInspector(root)
	if !workspacePresent(inspector) {
		return
	}
	var button js.Value
	switch id {
	case "edit.duplicate":
		button = inspector.Call("querySelector", "[data-placement-command=duplicate]")
	case "edit.delete":
		if root.Call("querySelectorAll", "#view-panel-arrange [data-placement-block]").Length() <= 1 {
			return
		}
		button = inspector.Call("querySelector", "[data-placement-command=delete]")
	case "transport.loop":
		var state struct {
			Listening *struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
				Loop bool   `json:"loop"`
			} `json:"listening"`
		}
		if !readNativeProjection(ctx, "/api/transport", &state) {
			return
		}
		// Recheck after the request; managed navigation may have changed the
		// selection, form revision, or focused editor in the meantime.
		current := selectedPlacementInspector(root)
		if !workspacePresent(current) || !current.Equal(inspector) {
			return
		}
		if state.Listening != nil && state.Listening.Kind == "placement" && state.Listening.Name == workspaceAttr(inspector, "data-placement-inspector") && state.Listening.Loop {
			button = root.Call("querySelector", "#view-panel-arrange .session-listening button[name=action][value=stop]")
		} else {
			forms := inspector.Call("querySelectorAll", ".placement-listening form")
			for i := 0; i < forms.Length(); i++ {
				form := forms.Index(i)
				if workspaceFieldValue(form, "mode") == "context" && workspaceFieldValue(form, "loop") == "true" {
					button = form.Call("querySelector", "button[type=submit]")
					break
				}
			}
		}
	}
	if ctx.Err() != nil || !root.Get("isConnected").Bool() || !workspacePresent(button) || button.Get("disabled").Bool() {
		return
	}
	form := button.Call("closest", "form")
	if workspacePresent(form) && form.Get("isConnected").Bool() {
		form.Call("requestSubmit", button)
	}
}
