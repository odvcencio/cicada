//go:build js && wasm

package main

import (
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// A placement GET can already be in flight when the user changes a local tab.
// Its older shell defaults must not replace that newer choice on arrival.
// Keep only choice strings across engine remounts, never nodes or callbacks.
const shellNavigationIntentKey = "__cicadaShellNavigationIntent"

func rememberPendingShellChoice(root, event js.Value) {
	if event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("shiftKey").Bool() || event.Get("altKey").Bool() {
		return
	}
	target := event.Get("target")
	if !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction || !root.Call("contains", target).Bool() {
		return
	}
	name, value := "", ""
	pending := workspaceAttr(js.Global().Get("document").Get("documentElement"), "data-gosx-navigation-state") == "pending" || workspacePresent(root.Call("querySelector", "[data-gosx-form-state=pending]"))
	if pending {
		closePane := target.Call("closest", `[data-pane="library"][data-pane-action="hide"]`)
		summary := target.Call("closest", "#browser-sampler > summary, #browser-takes > summary, #studio-browser > details > summary")
		activation := js.Global().Get("__cicadaRevealActivation")
		closingTarget := false
		if workspacePresent(summary) && summary.Get("parentElement").Get("open").Bool() {
			parent := summary.Get("parentElement")
			closingTarget = workspaceAttr(parent.Get("parentElement"), "id") == "studio-browser" || (workspacePresent(activation) && workspaceAttr(parent, "id") == "browser-"+workspaceString(activation, "panel"))
		}
		if workspacePresent(closePane) || closingTarget {
			intent := js.Global().Get(shellNavigationIntentKey)
			if !workspacePresent(intent) {
				intent = js.Global().Get("Object").New()
				js.Global().Set(shellNavigationIntentKey, intent)
			}
			intent.Set("suppressReveal", true)
			return
		}
	}
	if tab := target.Call("closest", "[data-gosx-tab]"); workspacePresent(tab) {
		group := tab.Call("closest", "[data-gosx-tabs-signal]")
		switch workspaceAttr(group, "data-gosx-tabs-signal") {
		case "$view":
			name = "view"
		case "$dock":
			name = "dock"
		}
		value = workspaceAttr(tab, "data-gosx-tab")
	}
	if phone := target.Call("closest", ".shell-phone-nav [data-shell-region]"); workspacePresent(phone) {
		name, value = "region", workspaceAttr(phone, "data-shell-region")
	}
	window := js.Global()
	if name == "" {
		// An explicit page destination supersedes an earlier local choice.
		if link := target.Call("closest", "a[href]"); workspacePresent(link) {
			if href := workspaceAttr(link, "href"); href != "" && href[0] != '#' {
				window.Set(shellNavigationIntentKey, js.Undefined())
			}
		}
		return
	}
	if !pending {
		window.Set(shellNavigationIntentKey, js.Undefined())
		return
	}
	intent := window.Get(shellNavigationIntentKey)
	if !workspacePresent(intent) {
		intent = window.Get("Object").New()
		window.Set(shellNavigationIntentKey, intent)
	}
	intent.Set(name, value)
}

func restorePendingShellChoice(host enginewasm.Context, root js.Value) {
	window := js.Global()
	intent := window.Get(shellNavigationIntentKey)
	if !workspacePresent(intent) || !root.Get("isConnected").Bool() {
		return
	}
	window.Set(shellNavigationIntentKey, js.Undefined())
	url := shellCurrentURL()
	if intent.Get("suppressReveal").Type() == js.TypeBoolean && intent.Get("suppressReveal").Bool() {
		url.Get("searchParams").Call("delete", "reveal")
	}
	region := workspaceString(intent, "region")
	for _, allowed := range []string{"browser", "center", "detail", "code"} {
		if region == allowed {
			root.Call("setAttribute", "data-phone-region", region)
			url.Get("searchParams").Call("set", "region", region)
			url.Get("searchParams").Call("delete", "reveal")
			break
		}
	}
	for name, choices := range map[string][]string{"view": {"arrange", "session", "mixer"}, "dock": {"clip", "device", "automation"}} {
		value := workspaceString(intent, name)
		for _, allowed := range choices {
			if value != allowed {
				continue
			}
			key := name
			_ = host.SetSignal("$"+name, value)
			if name == "view" {
				key = "tab"
				root.Call("querySelector", "#studio-center").Call("setAttribute", "data-studio-region", value)
				if value != "arrange" {
					url.Set("hash", "")
				}
			}
			url.Get("searchParams").Call("set", key, value)
			break
		}
	}
	syncShellLocation(root, url)
}
