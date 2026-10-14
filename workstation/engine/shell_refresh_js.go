//go:build js && wasm

package main

import (
	"syscall/js"

	"m31labs.dev/gosx/signal"
)

// watchShellRefresh gives the /studio shell the refresh consumer the panel
// application gets from the workspace engine. Engines that save (sampler
// resample, retained Live note takes, finished takes) bump
// workspaceRefreshRequested. The shell has no workspace engine, so one watcher
// asks GoSX's managed navigation to revalidate the current URL: the server
// renders the shell again, and GoSX reconciles the page, so the score,
// the review controls and every revision field advance together. The panel
// application keeps its own consumer and is untouched.
//
// S2 replaces the bump with hub telemetry.
func watchShellRefresh() {
	seen := workspaceRefreshRequested.Get()
	signal.Watch(func() {
		requested := workspaceRefreshRequested.Get()
		if requested == seen {
			return
		}
		seen = requested
		if js.Global().Get("location").Get("pathname").String() != "/studio" {
			return
		}
		navigation := js.Global().Get("__gosx").Get("host").Get("navigation")
		if navigation.Type() == js.TypeObject && navigation.Get("revalidate").Type() == js.TypeFunction {
			navigation.Call("revalidate")
			return
		}
		js.Global().Get("location").Call("reload")
	})
}
