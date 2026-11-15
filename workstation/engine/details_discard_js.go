//go:build js && wasm

package main

import "syscall/js"

func bindDetailsDiscard(root js.Value) func() {
	document := js.Global().Get("document")
	selector := "details[data-discard-on-close]"
	toggle := js.FuncOf(func(_ js.Value, args []js.Value) any {
		target := args[0].Get("target")
		if !workspacePresent(target) || target.Get("matches").Type() != js.TypeFunction || !target.Call("matches", selector).Bool() || !root.Call("contains", target).Bool() || target.Get("open").Bool() {
			return nil
		}
		forms := target.Call("querySelectorAll", "form")
		for i := 0; i < forms.Length(); i++ {
			forms.Index(i).Call("reset")
		}
		controls := target.Call("querySelectorAll", "[data-edited], [data-baseline]")
		for i := 0; i < controls.Length(); i++ {
			controls.Index(i).Call("removeAttribute", "data-edited")
			controls.Index(i).Call("removeAttribute", "data-baseline")
		}
		return nil
	})
	keydown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		target := event.Get("target")
		if event.Get("key").String() != "Escape" || !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
			return nil
		}
		details := target.Call("closest", selector+"[open]")
		if workspacePresent(details) && root.Call("contains", details).Bool() {
			details.Set("open", false)
			if summary := details.Call("querySelector", "summary"); workspacePresent(summary) {
				summary.Call("focus")
			}
			event.Call("preventDefault")
			event.Call("stopPropagation")
		}
		return nil
	})
	// Only floating panels close on an outside press. A form that sits in the
	// page flow (track rename) would shift the layout between press and release,
	// and the click would miss what the user pressed.
	pointerdown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		target := args[0].Get("target")
		panels := root.Call("querySelectorAll", selector+"[data-dismiss-outside][open]")
		for i := 0; i < panels.Length(); i++ {
			panel := panels.Index(i)
			if !panel.Call("contains", target).Bool() {
				panel.Set("open", false)
			}
		}
		return nil
	})
	document.Call("addEventListener", "toggle", toggle, true)
	document.Call("addEventListener", "keydown", keydown, true)
	document.Call("addEventListener", "pointerdown", pointerdown, true)
	return func() {
		document.Call("removeEventListener", "toggle", toggle, true)
		document.Call("removeEventListener", "keydown", keydown, true)
		document.Call("removeEventListener", "pointerdown", pointerdown, true)
		toggle.Release()
		keydown.Release()
		pointerdown.Release()
	}
}
