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
// reloads the current /studio URL: the server renders the shell again, so the
// score, the review controls and every revision field advance together. The
// URL carries the view, the inspected file, pattern and step; the Code lens
// draft and the retained note take live in the server session and come back
// with the page. I looked for a lighter in-place refresh in the pinned GoSX
// runtime and did not rely on one (see the PR notes).
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
		js.Global().Get("location").Call("reload")
	})
}
