//go:build js && wasm

package main

import "syscall/js"

// The framework's read-only export region owns fetching and live bindings.
// Mirror its busy attribute to the form outside that region, so focused form
// controls cannot suspend progress and repeated renders remain disabled.
func watchExportBusy(root js.Value) func() {
	stopMix := watchExportButton(root, "#export-result", "#export-render-button")
	stopStems := watchExportButton(root, "#stems-export-result", "#stems-render-button")
	return func() { stopMix(); stopStems() }
}

func watchExportButton(root js.Value, resultSelector, buttonSelector string) func() {
	result := root.Call("querySelector", resultSelector)
	if !workspacePresent(result) {
		return func() {}
	}
	button := root.Call("querySelector", buttonSelector)
	if !workspacePresent(button) {
		return func() {}
	}
	form := button.Call("closest", "form")
	update := func() {
		pending := workspacePresent(form) && workspaceAttr(form, "data-gosx-pending") == "true"
		disabled := pending || workspaceAttr(result, "aria-busy") == "true"
		if button.Get("disabled").Bool() != disabled {
			button.Set("disabled", disabled)
		}
	}
	callback := js.FuncOf(func(js.Value, []js.Value) any { update(); return nil })
	observer := js.Global().Get("MutationObserver").New(callback)
	observer.Call("observe", result, map[string]any{"attributes": true, "attributeFilter": []any{"aria-busy"}})
	if workspacePresent(form) {
		observer.Call("observe", form, map[string]any{"attributes": true, "attributeFilter": []any{"data-gosx-pending"}})
	}
	update()
	return func() { observer.Call("disconnect"); callback.Release() }
}
