//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

const arrangeSectionStorage = "cicada:arrange:section"

var rememberedArrangeSection string

// Storage can be unavailable in private browsing or under browser policies.
func arrangeSectionStorageValue(action, value string) (result string) {
	switch action {
	case "get":
		if rememberedArrangeSection != "" {
			return rememberedArrangeSection
		}
	case "set":
		rememberedArrangeSection = value
	case "clear":
		rememberedArrangeSection = ""
	}
	defer func() { _ = recover() }()
	storage := js.Global().Get("sessionStorage")
	switch action {
	case "get":
		if saved := storage.Call("getItem", arrangeSectionStorage); saved.Type() == js.TypeString {
			rememberedArrangeSection = saved.String()
			return rememberedArrangeSection
		}
	case "set":
		storage.Call("setItem", arrangeSectionStorage, value)
	case "clear":
		storage.Call("removeItem", arrangeSectionStorage)
	}
	return ""
}

func arrangeSectionKey(block js.Value) string {
	return workspaceAttr(block, "data-song-index") + "|" + workspaceAttr(block, "data-song-key") + "|" + workspaceAttr(block, "data-song-generation")
}

func discardArrangeSection(root, blocks js.Value) {
	for i := 0; i < blocks.Length(); i++ {
		block := blocks.Index(i)
		block.Set("open", false)
		discardDetailsDraft(block)
		block.Call("removeAttribute", "data-song-selected")
	}
	if status := root.Call("querySelector", "#arrangement [data-scene-open-status]"); workspacePresent(status) {
		status.Set("textContent", "The song changed. Choose Edit on a block to continue editing it.")
	}
	arrangeSectionStorageValue("clear", "")
}

func restoreArrangeSection(root js.Value) {
	if !workspacePresent(root.Call("querySelector", "#arrangement")) {
		return
	}
	blocks := root.Call("querySelectorAll", "#arrangement details.arrangement-block[data-song-key]")
	for i := 0; i < blocks.Length(); i++ {
		if block := blocks.Index(i); block.Get("open").Bool() {
			arrangeSectionStorageValue("set", arrangeSectionKey(block))
			return
		}
	}
	if saved := arrangeSectionStorageValue("get", ""); saved != "" {
		for i := 0; i < blocks.Length(); i++ {
			block := blocks.Index(i)
			if arrangeSectionKey(block) == saved {
				block.Call("setAttribute", "data-song-selected", "")
				block.Set("open", true)
				return
			}
		}
		discardArrangeSection(root, blocks)
		return
	}
}

func checkArrangeSection(root js.Value) {
	if saved := arrangeSectionStorageValue("get", ""); saved != "" {
		if !workspacePresent(root.Call("querySelector", "#arrangement")) {
			return
		}
		blocks := root.Call("querySelectorAll", "#arrangement details.arrangement-block[data-song-key]")
		found := false
		for i := 0; i < blocks.Length(); i++ {
			block := blocks.Index(i)
			if arrangeSectionKey(block) == saved {
				found = true
			} else if block.Get("open").Bool() {
				found = false
				break
			}
		}
		if !found {
			discardArrangeSection(root, blocks)
		}
	}
}

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
	document := js.Global().Get("document")
	remember := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && root.Get("isConnected").Bool() {
			rememberRevealActivation(args[0])
			rememberPendingShellChoice(root, args[0])
		}
		return nil
	})
	// Wait for the managed navigation to finish before changing the visible
	// dock: it can be refused while the user has unsaved work.
	pendingScenePatternURL := ""
	restore := js.FuncOf(func(js.Value, []js.Value) any {
		restorePendingShellChoice(host, root)
		if pendingScenePatternURL != "" && root.Get("isConnected").Bool() && shellCurrentURL().Get("href").String() == pendingScenePatternURL {
			_ = host.SetSignal("$dock", "clip")
			root.Call("setAttribute", "data-phone-region", "detail")
		}
		pendingScenePatternURL = ""
		revealShellBrowserDestination(host, root)
		return nil
	})
	document.Call("addEventListener", "click", remember, true)
	document.Call("addEventListener", "gosx:navigate", restore)
	focusChanged := js.FuncOf(func(js.Value, []js.Value) any {
		intent := js.Global().Get("__cicadaRevealActivation")
		if workspacePresent(intent) {
			intent.Set("focusChanged", true)
		}
		return nil
	})
	document.Call("addEventListener", "focusin", focusChanged, true)
	// Keep can commit before the refreshed history forms arrive. Block direct
	// form submits while the history lock holds (history_lock.go).
	historyPending := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			event := args[0]
			form := event.Get("target")
			if workspacePresent(form) && form.Get("hasAttribute").Type() == js.TypeFunction && (workspaceAttr(form, "id") == "undo-form" || workspaceAttr(form, "id") == "redo-form") && history.Locked() {
				event.Call("preventDefault")
				event.Call("stopImmediatePropagation")
			}
		}
		return nil
	})
	document.Call("addEventListener", "submit", historyPending, true)
	restorePendingShellChoice(host, root)
	revealShellBrowserDestination(host, root)
	restoreArrangeSection(root)
	removeArrangeUpdated := workspaceUpdated.add(func() {
		if root.Get("isConnected").Bool() {
			checkArrangeSection(root)
		}
	})
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
		pendingScenePatternURL = ""
		if summary := target.Call("closest", "#arrangement details.arrangement-block > summary"); workspacePresent(summary) {
			block := summary.Get("parentElement")
			if block.Get("open").Bool() {
				arrangeSectionStorageValue("clear", "")
				discardDetailsDraft(block)
				block.Call("removeAttribute", "data-song-selected")
			}
		}
		// Reveal the editor only after this link's destination has loaded.
		if link := target.Call("closest", "[data-scene-pattern-open]"); workspacePresent(link) {
			reason := ""
			switch {
			case nativeCapture.Get().State.Active != "":
				reason = "Finish recording before opening a pattern."
			case workspacePresent(root.Call("querySelector", "[data-gosx-form-state=pending]")), workspaceAttr(document.Get("documentElement"), "data-gosx-navigation-state") == "pending":
				reason = "Wait for the current action to finish before opening a pattern."
			case hasUnsavedWork():
				reason = "Save or discard unsaved edits before opening a pattern."
			}
			if reason != "" {
				event.Call("preventDefault")
				if status := root.Call("querySelector", "[data-scene-open-status]"); workspacePresent(status) {
					status.Set("textContent", reason)
				}
				return nil
			}
			pendingScenePatternURL = link.Get("href").String()
			return nil
		}
		// Selecting a scene block uses the already-rendered forms: it must not
		// navigate away from a Live engine that may be holding notes.
		edit := target.Call("closest", ".timeline-edit[data-song-index]")
		if workspacePresent(edit) {
			index := workspaceAttr(edit, "data-song-index")
			blocks := root.Call("querySelectorAll", ".arrangement-block[data-song-index]")
			var selected js.Value
			for i := 0; i < blocks.Length(); i++ {
				block := blocks.Index(i)
				if workspaceAttr(block, "data-song-index") == index {
					selected = block
					continue
				}
				block.Call("removeAttribute", "data-song-selected")
				block.Set("open", false)
				discardDetailsDraft(block)
			}
			if workspacePresent(selected) {
				event.Call("preventDefault")
				arrangeSectionStorageValue("set", arrangeSectionKey(selected))
				if status := root.Call("querySelector", "#arrangement [data-scene-open-status]"); workspacePresent(status) {
					status.Set("textContent", "")
				}
				selected.Call("setAttribute", "data-song-selected", "")
				selected.Set("open", true)
				summary := selected.Call("querySelector", "summary")
				if workspacePresent(summary) {
					summary.Call("scrollIntoView", map[string]any{"block": "nearest", "inline": "nearest", "behavior": "auto"})
					if event.Get("detail").Int() == 0 {
						summary.Call("focus", map[string]any{"preventScroll": true})
					}
				}
			}
			return nil
		}
		// Managed navigation retains the shell controller, so an explicit URL
		// alone does not switch the existing dock signal. Reveal Automation
		// before following the catalog-selected amp curve link.
		if workspacePresent(target.Call("closest", "[data-amp-automation-link]")) {
			_ = host.SetSignal("$dock", "automation")
			return nil
		}
		// A Mixer strip's name opens that channel in Device.
		if choice := target.Call("closest", "[data-mixer-select]"); workspacePresent(choice) {
			_ = host.SetSignal("$dock", "device")
			if js.Global().Get("innerWidth").Float() < 800 {
				root.Call("setAttribute", "data-phone-region", "detail")
				url := shellCurrentURL()
				url.Get("searchParams").Call("set", "region", "detail")
				syncShellLocation(root, url)
			}
			// The server's link names only the channel. Follow it with the
			// rest of the page's selections (pattern, placement, file) kept.
			url := shellCurrentURL()
			for key, value := range map[string]string{"tab": "mixer", "dock": "device", "deviceTrack": workspaceAttr(choice, "data-mixer-select")} {
				url.Get("searchParams").Call("set", key, value)
			}
			choice.Call("setAttribute", "href", url.Get("pathname").String()+url.Get("search").String())
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
				if region == "browser" {
					_ = host.SetSignal("$layout.browserOpen", true)
				}
				if region == "code" {
					_ = host.SetSignal("$layout.codeOpen", true)
				}
				root.Call("setAttribute", "data-phone-region", region)
				url := shellCurrentURL()
				if region != "browser" {
					url.Get("searchParams").Call("delete", "reveal")
				}
				url.Get("searchParams").Call("set", "region", region)
				syncShellLocation(root, url)
				break
			}
		}
		return nil
	})
	root.Call("addEventListener", "click", localClick)
	// Native controls own their activation keys. In particular, Space on a
	// sidebar summary must collapse it instead of toggling song playback.
	nativeKeys := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		event := args[0]
		key := event.Get("key").String()
		if (key != " " && key != "Enter") || event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() || event.Get("altKey").Bool() || event.Get("shiftKey").Bool() {
			return nil
		}
		target := event.Get("target")
		if workspacePresent(target) && target.Get("closest").Type() == js.TypeFunction && workspacePresent(target.Call("closest", "button,summary")) {
			event.Call("stopPropagation")
		}
		return nil
	})
	root.Call("addEventListener", "keydown", nativeKeys)
	return func() {
		document.Call("removeEventListener", "click", remember, true)
		document.Call("removeEventListener", "gosx:navigate", restore)
		document.Call("removeEventListener", "focusin", focusChanged, true)
		document.Call("removeEventListener", "submit", historyPending, true)
		removeArrangeUpdated()
		historyPending.Release()
		remember.Release()
		restore.Release()
		focusChanged.Release()
		for _, release := range releases {
			release()
		}
		root.Call("removeEventListener", "click", localClick)
		root.Call("removeEventListener", "keydown", nativeKeys)
		nativeKeys.Release()
		localClick.Release()
	}, nil
}

func rememberRevealActivation(event js.Value) {
	target := event.Get("target")
	if !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
		return
	}
	link := target.Call("closest", "a[href]")
	if !workspacePresent(link) {
		return
	}
	url := js.Global().Get("URL").New(link.Get("href"), js.Global().Get("location").Get("href"))
	panel := url.Get("searchParams").Call("get", "reveal")
	if panel.IsNull() || (panel.String() != "sampler" && panel.String() != "takes") {
		js.Global().Set("__cicadaRevealActivation", js.Undefined())
		return
	}
	intent := js.Global().Get("Object").New()
	intent.Set("keyboard", event.Get("detail").Int() == 0)
	intent.Set("panel", panel.String())
	intent.Set("focusChanged", false)
	js.Global().Set("__cicadaRevealActivation", intent)
}

// Apply a destination only after navigation succeeds (or when its URL is
// loaded directly), so a blocked navigation never changes the current layout.
func revealShellBrowserDestination(host enginewasm.Context, root js.Value) {
	if !root.Get("isConnected").Bool() {
		return
	}
	url := shellCurrentURL()
	panel := url.Get("searchParams").Call("get", "reveal")
	if panel.IsNull() || (panel.String() != "sampler" && panel.String() != "takes") {
		js.Global().Set("__cicadaRevealActivation", js.Undefined())
		return
	}
	activation := js.Global().Get("__cicadaRevealActivation")
	js.Global().Set("__cicadaRevealActivation", js.Undefined())
	_ = host.SetSignal("$layout.browserOpen", true)
	_ = host.SetSignal("$layout."+panel.String()+"Open", true)
	root.Call("setAttribute", "data-phone-region", "browser")
	url.Get("searchParams").Call("set", "region", "browser")
	// The signals persist the user's resulting layout. Consume the instruction
	// so a later explicit close remains closed after reload.
	url.Get("searchParams").Call("delete", "reveal")
	syncShellLocation(root, url)
	document := js.Global().Get("document")
	focus := document.Get("activeElement")
	var reveal js.Func
	reveal = js.FuncOf(func(js.Value, []js.Value) any {
		defer reveal.Release()
		if !root.Get("isConnected").Bool() || !document.Get("activeElement").Equal(focus) || workspaceAttr(root, "data-phone-region") != "browser" {
			return nil
		}
		section := root.Call("querySelector", "#browser-"+panel.String()+" > summary")
		if workspacePresent(section) {
			section.Call("scrollIntoView", map[string]any{"block": "nearest", "inline": "nearest", "behavior": "auto"})
			if workspacePresent(activation) && activation.Get("keyboard").Bool() && !activation.Get("focusChanged").Bool() {
				section.Call("focus", map[string]any{"preventScroll": true})
			}
		}
		return nil
	})
	js.Global().Call("requestAnimationFrame", reveal)
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
		if link.Call("hasAttribute", "data-mixer-select").Bool() {
			target.Get("searchParams").Call("set", "tab", "mixer")
			target.Get("searchParams").Call("set", "dock", "device")
		}
		// A scene clip opens its pattern in the Clip dock, whichever dock or
		// phone region is showing now.
		if link.Call("hasAttribute", "data-scene-pattern-open").Bool() {
			target.Get("searchParams").Call("set", "dock", "clip")
			target.Get("searchParams").Call("set", "region", "detail")
		}
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
		if key == "region" && !target.Get("searchParams").Call("get", "reveal").IsNull() {
			continue
		}
		value := current.Get("searchParams").Call("get", key)
		if value.IsNull() {
			target.Get("searchParams").Call("delete", key)
		} else {
			target.Get("searchParams").Call("set", key, value)
		}
	}
}
