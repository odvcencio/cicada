//go:build js && wasm

package main

import (
	"context"
	"syscall/js"
	"time"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// Reveal the existing recording forms without navigating or replacing drafts.
// Record still submits their ordinary guarded action when recording is allowed.
func revealVocalWorkspace(ctx context.Context, host enginewasm.Context, root js.Value) bool {
	vocals := root.Call("querySelector", "#vocals")
	arrange := root.Call("querySelector", "#view-panel-arrange")
	if !workspacePresent(vocals) || !workspacePresent(arrange) {
		return false
	}
	if arrange.Get("hidden").Bool() {
		if err := host.SetSignal("$view", "arrange"); err != nil {
			return false
		}
	}
	deadline := time.Now().Add(time.Second)
	for arrange.Get("hidden").Bool() {
		if ctx.Err() != nil || !root.Get("isConnected").Bool() || time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil || !root.Get("isConnected").Bool() {
		return false
	}

	// Phone regions are server-rendered. Keep their URL and navigation links
	// consistent with the revealed pane so subsequent form returns stay here.
	root.Call("setAttribute", "data-phone-region", "center")
	url := shellCurrentURL()
	url.Get("searchParams").Call("set", "region", "center")
	url.Get("searchParams").Call("set", "tab", "arrange")
	url.Set("hash", "vocals")
	syncShellLocation(root, url)
	vocals.Call("scrollIntoView", map[string]any{"block": "start", "behavior": "auto"})
	return true
}
