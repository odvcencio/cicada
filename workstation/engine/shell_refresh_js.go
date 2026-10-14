//go:build js && wasm

package main

import (
	"sync"
	"syscall/js"

	"m31labs.dev/gosx/signal"
)

// Engines that hold input the server has not seen register a check here. A
// refresh never discards unsaved work: while any check reports work, or a form
// control on the page differs from what the server rendered, the shell shows a
// notice instead of reloading.
var unsavedWork sync.Map // map[string]func() bool

// holdUnsavedWork registers an engine's check and returns its removal.
func holdUnsavedWork(name string, dirty func() bool) (release func()) {
	unsavedWork.Store(name, dirty)
	return func() { unsavedWork.Delete(name) }
}

// markEdited tags a control the user changed. Engines also set control values
// when they mount, so only controls that received a real input event count.
func markEdited(event js.Value) {
	if !event.Get("isTrusted").Bool() {
		return
	}
	if target := event.Get("target"); target.Type() == js.TypeObject && target.Get("setAttribute").Type() == js.TypeFunction {
		target.Call("setAttribute", "data-edited", "true")
	}
}

// controlsDirty reports an edited text, number, select, checkbox or textarea
// control whose current value still differs from the rendered one, for example
// Code lens text typed since the last save.
func controlsDirty() bool {
	root := js.Global().Get("document").Call("getElementById", "studio-shell")
	if root.IsNull() {
		return false
	}
	controls := root.Call("querySelectorAll", "[data-edited]")
	for i := 0; i < controls.Length(); i++ {
		control := controls.Index(i)
		switch tag := control.Get("tagName").String(); {
		case tag == "SELECT":
			options := control.Get("options")
			for j := 0; j < options.Length(); j++ {
				option := options.Index(j)
				if option.Get("selected").Bool() != option.Get("defaultSelected").Bool() {
					return true
				}
			}
		case tag == "INPUT" && (control.Get("type").String() == "checkbox" || control.Get("type").String() == "radio"):
			if control.Get("checked").Bool() != control.Get("defaultChecked").Bool() {
				return true
			}
		default:
			if control.Get("value").String() != control.Get("defaultValue").String() {
				return true
			}
		}
	}
	return false
}

func hasUnsavedWork() bool {
	dirty := controlsDirty()
	unsavedWork.Range(func(_, check any) bool {
		if check.(func() bool)() {
			dirty = true
		}
		return !dirty
	})
	return dirty
}

// refreshShell reloads the current /studio URL when nothing is unsaved, so the
// score, the review controls and every revision field advance together. The
// view, file, pattern and step are in the URL; the retained note take and failed
// source drafts live in the server session. When something is unsaved it shows
// the page's refresh notice and leaves everything in place. The revision fields
// keep their old value, which the service now rejects as stale, so a later save
// cannot silently overwrite the newer score.
func refreshShell() {
	if js.Global().Get("location").Get("pathname").String() != "/studio" {
		return
	}
	if hasUnsavedWork() {
		if notice := js.Global().Get("document").Call("getElementById", "studio-refresh-notice"); !notice.IsNull() {
			notice.Set("hidden", false)
		}
		return
	}
	js.Global().Get("location").Call("reload")
}

// watchShellRefresh gives the /studio shell the refresh consumer the panel
// application gets from the workspace engine. Engines that save (sampler
// resample, retained Live note takes, finished takes) bump
// workspaceRefreshRequested; a "cicada:shell-refresh" event on the document asks
// for the same thing. S2 replaces the bump with hub telemetry.
func watchShellRefresh() {
	seen := workspaceRefreshRequested.Get()
	signal.Watch(func() {
		requested := workspaceRefreshRequested.Get()
		if requested == seen {
			return
		}
		seen = requested
		refreshShell()
	})
	for _, name := range []string{"input", "change"} {
		js.Global().Get("document").Call("addEventListener", name, js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) > 0 {
				markEdited(args[0])
			}
			return nil
		}), true)
	}
	js.Global().Get("document").Call("addEventListener", "cicada:shell-refresh", js.FuncOf(func(js.Value, []js.Value) any {
		refreshShell()
		return nil
	}))
}
