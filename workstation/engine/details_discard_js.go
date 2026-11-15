//go:build js && wasm

package main

import "syscall/js"

// pressOnWorkspaceStatus reports whether a press landed inside the box of a
// workspace status message that shows text.
func pressOnWorkspaceStatus(event js.Value) bool {
	x, y := event.Get("clientX"), event.Get("clientY")
	if x.Type() != js.TypeNumber || y.Type() != js.TypeNumber {
		return false
	}
	nodes := js.Global().Get("document").Call("querySelectorAll", ".workspace-feedback [data-workspace-status]")
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		if node.Get("textContent").String() == "" {
			continue
		}
		box := node.Call("getBoundingClientRect")
		if box.Get("width").Float() > 0 && x.Float() >= box.Get("left").Float() && x.Float() <= box.Get("right").Float() && y.Float() >= box.Get("top").Float() && y.Float() <= box.Get("bottom").Float() {
			return true
		}
	}
	return false
}

func discardDetailsDraft(details js.Value) {
	forms := details.Call("querySelectorAll", "form")
	for i := 0; i < forms.Length(); i++ {
		form := forms.Index(i)
		form.Call("reset")
		// A refusal shown beside the discarded values would describe values that are gone.
		if form.Call("getAttribute", "data-gosx-form-state").String() == "error" {
			form.Call("setAttribute", "data-gosx-form-state", "idle")
		}
		if status := form.Call("querySelector", ".form-status"); workspacePresent(status) {
			status.Set("textContent", "")
		}
	}
	controls := details.Call("querySelectorAll", "[data-edited], [data-baseline]")
	for i := 0; i < controls.Length(); i++ {
		controls.Index(i).Call("removeAttribute", "data-edited")
		controls.Index(i).Call("removeAttribute", "data-baseline")
	}
}

func bindDetailsDiscard(root js.Value) func() {
	document := js.Global().Get("document")
	selector := "details[data-discard-on-close]"
	toggle := js.FuncOf(func(_ js.Value, args []js.Value) any {
		target := args[0].Get("target")
		if !workspacePresent(target) || target.Get("matches").Type() != js.TypeFunction || !target.Call("matches", selector).Bool() || !root.Call("contains", target).Bool() || target.Get("open").Bool() {
			return nil
		}
		discardDetailsDraft(target)
		return nil
	})
	keydown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		target := event.Get("target")
		if event.Get("isComposing").Bool() || event.Get("key").String() != "Escape" || !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
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
	click := js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		if event.Get("button").Int() != 0 {
			return nil
		}
		target := event.Get("target")
		// A press inside the strip that holds the panels ([data-dismiss-scope]) or
		// on a message (the refusal toast and its close button) belongs to the same
		// task, so it keeps the draft. Another panel's summary still closes this
		// one, through the shared name of the details group.
		if workspacePresent(target) && target.Get("closest").Type() == js.TypeFunction && workspacePresent(target.Call("closest", "[data-dismiss-scope], [data-gosx-toast-host], .workspace-feedback")) {
			return nil
		}
		// The status message takes no pointer events (the phone navigation sits
		// under it), so a press on it arrives at what lies beneath. A press
		// inside the message's box still belongs to the same task.
		if pressOnWorkspaceStatus(event) {
			return nil
		}
		panels := root.Call("querySelectorAll", selector+"[data-dismiss-outside][open]")
		for i := 0; i < panels.Length(); i++ {
			panel := panels.Index(i)
			if !panel.Call("contains", target).Bool() {
				panel.Set("open", false)
				discardDetailsDraft(panel)
			}
		}
		return nil
	})
	document.Call("addEventListener", "toggle", toggle, true)
	document.Call("addEventListener", "keydown", keydown, true)
	document.Call("addEventListener", "click", click, true)
	return func() {
		document.Call("removeEventListener", "toggle", toggle, true)
		document.Call("removeEventListener", "keydown", keydown, true)
		document.Call("removeEventListener", "click", click, true)
		toggle.Release()
		keydown.Release()
		click.Release()
	}
}
