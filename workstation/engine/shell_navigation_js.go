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
	phoneClick := js.FuncOf(func(_ js.Value, args []js.Value) any {
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
	root.Call("addEventListener", "click", phoneClick)
	return func() {
		for _, release := range releases {
			release()
		}
		root.Call("removeEventListener", "click", phoneClick)
		phoneClick.Release()
	}, nil
}

func shellCurrentURL() js.Value {
	return js.Global().Get("URL").New(js.Global().Get("location").Get("href"))
}

func syncShellLocation(root, url js.Value) {
	window := js.Global()
	window.Get("history").Call("replaceState", window.Get("history").Get("state"), "", url.Get("href"))
	links := root.Call("querySelectorAll", ".shell-phone-nav [data-shell-region], [data-gosx-tabs] [data-gosx-tab]")
	for i := 0; i < links.Length(); i++ {
		link := links.Index(i)
		target := window.Get("URL").New(url.Get("href"))
		if region := workspaceAttr(link, "data-shell-region"); region != "" {
			if region == workspaceAttr(root, "data-phone-region") {
				link.Call("setAttribute", "aria-current", "page")
			} else {
				link.Call("removeAttribute", "aria-current")
			}
			target.Get("searchParams").Call("set", "region", region)
		} else {
			container := link.Call("closest", "[data-gosx-tabs]")
			key := "dock"
			if workspaceAttr(container, "data-gosx-tabs-signal") == "$view" {
				key = "tab"
			}
			target.Get("searchParams").Call("set", key, workspaceAttr(link, "data-gosx-tab"))
		}
		target.Set("hash", "")
		link.Call("setAttribute", "href", target.Get("pathname").String()+target.Get("search").String())
	}
}
