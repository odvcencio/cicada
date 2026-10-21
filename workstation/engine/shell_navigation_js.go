//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// Tab recipes own visibility and keyboard focus. Their shared selections also
// belong in the URL so saved forms, refreshes and modified clicks keep context.
func bindShellNavigation(host enginewasm.Context, root js.Value) (func(), error) {
	var releases []enginewasm.HandleFunc
	for name, choices := range map[string][]string{"view": {"arrange", "session", "mixer"}, "dock": {"clip", "device", "automation"}} {
		release, err := host.SubscribeSignal("$"+name, func(raw json.RawMessage) {
			var value string
			if json.Unmarshal(raw, &value) != nil || !root.Get("isConnected").Bool() {
				return
			}
			for _, choice := range choices {
				if value != choice {
					continue
				}
				url := shellCurrentURL()
				key := name
				if name == "view" {
					key = "tab"
					root.Call("querySelector", "#studio-center").Call("setAttribute", "data-studio-region", value)
					if value != "arrange" {
						url.Set("hash", "")
					}
				}
				url.Get("searchParams").Call("set", key, value)
				syncShellLocation(root, url)
				return
			}
		})
		if err != nil {
			for _, previous := range releases {
				previous()
			}
			return nil, err
		}
		releases = append(releases, release)
	}
	localClick := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 || !root.Get("isConnected").Bool() {
			return nil
		}
		event := args[0]
		if event.Get("defaultPrevented").Bool() || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("shiftKey").Bool() || event.Get("altKey").Bool() {
			return nil
		}
		target := event.Get("target")
		if !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
			return nil
		}
		// Native fragment navigation emits popstate in WebKit and Chromium.
		// The navigation host can still remember the URL from before a local
		// tab switch and mistake that fragment for a different page. Reveal
		// these mounted workspaces directly, as the Record command does, so
		// moving within Arrange never disposes engines or unsaved drafts.
		jump := target.Call("closest", ".arrange-workspace-nav a[href]")
		if workspacePresent(jump) {
			href := workspaceAttr(jump, "href")
			if href == "#arrangement" || href == "#vocals" {
				pane := root.Call("querySelector", href)
				if workspacePresent(pane) {
					event.Call("preventDefault")
					url := shellCurrentURL()
					url.Set("hash", href)
					syncShellLocation(root, url)
					pane.Call("scrollIntoView", map[string]any{"block": "start", "behavior": "auto"})
				}
			}
			return nil
		}
		link := target.Call("closest", ".shell-phone-nav [data-shell-region]")
		if !workspacePresent(link) {
			return nil
		}
		region := workspaceAttr(link, "data-shell-region")
		for _, allowed := range []string{"browser", "center", "detail", "code"} {
			if region == allowed {
				event.Call("preventDefault")
				root.Call("setAttribute", "data-phone-region", region)
				url := shellCurrentURL()
				url.Get("searchParams").Call("set", "region", region)
				syncShellLocation(root, url)
				break
			}
		}
		return nil
	})
	root.Call("addEventListener", "click", localClick)
	return func() {
		for _, release := range releases {
			release()
		}
		root.Call("removeEventListener", "click", localClick)
		localClick.Release()
	}, nil
}

func shellCurrentURL() js.Value {
	return js.Global().Get("URL").New(js.Global().Get("location").Get("href"))
}

func syncShellLocation(root, url js.Value) {
	window := js.Global()
	window.Get("history").Call("replaceState", window.Get("history").Get("state"), "", url.Get("href"))
	links := root.Call("querySelectorAll", "a[href]")
	for i := 0; i < links.Length(); i++ {
		link := links.Index(i)
		href := workspaceAttr(link, "href")
		if href == "" || href[0] == '#' {
			continue
		}
		target := window.Get("URL").New(href, url.Get("href"))
		if !shellContextTarget(target, url) || target.Get("search").String() == "" {
			continue
		}
		copyShellContext(target, url)
		if region := workspaceAttr(link, "data-shell-region"); region != "" {
			if region == workspaceAttr(root, "data-phone-region") {
				link.Call("setAttribute", "aria-current", "page")
			} else {
				link.Call("removeAttribute", "aria-current")
			}
			target.Get("searchParams").Call("set", "region", region)
		} else if workspaceAttr(link, "data-gosx-tab") != "" {
			container := link.Call("closest", "[data-gosx-tabs]")
			key := "dock"
			if workspaceAttr(container, "data-gosx-tabs-signal") == "$view" {
				key = "tab"
			}
			target.Get("searchParams").Call("set", key, workspaceAttr(link, "data-gosx-tab"))
		}
		link.Call("setAttribute", "href", target.Get("pathname").String()+target.Get("search").String()+target.Get("hash").String())
	}
	// Managed form returns and GET pickers were rendered before the local tab
	// switch. Carry the same workspace context through their next server update.
	returns := root.Call("querySelectorAll", "input[name=__gosx_return_to]")
	for i := 0; i < returns.Length(); i++ {
		field := returns.Index(i)
		target := window.Get("URL").New(field.Get("value"), url.Get("href"))
		if shellContextTarget(target, url) {
			copyShellContext(target, url)
			field.Set("value", target.Get("pathname").String()+target.Get("search").String()+target.Get("hash").String())
		}
	}
	forms := root.Call("querySelectorAll", "form[method=get]")
	for i := 0; i < forms.Length(); i++ {
		form := forms.Index(i)
		if !shellContextTarget(window.Get("URL").New(form.Get("action")), url) {
			continue
		}
		for _, key := range []string{"tab", "dock", "region"} {
			field := form.Call("querySelector", "input[type=hidden][name="+key+"]")
			if !workspacePresent(field) {
				field = window.Get("document").Call("createElement", "input")
				field.Set("type", "hidden")
				field.Set("name", key)
				form.Call("appendChild", field)
			}
			value := url.Get("searchParams").Call("get", key)
			if value.IsNull() {
				value = js.ValueOf("")
			}
			field.Set("value", value)
		}
	}
}

func shellContextTarget(target, current js.Value) bool {
	return target.Get("origin").String() == current.Get("origin").String() && target.Get("pathname").String() == "/studio"
}

func copyShellContext(target, current js.Value) {
	for _, key := range []string{"tab", "dock", "region"} {
		value := current.Get("searchParams").Call("get", key)
		if value.IsNull() {
			target.Get("searchParams").Call("delete", key)
		} else {
			target.Get("searchParams").Call("set", key, value)
		}
	}
}
