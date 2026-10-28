package main

import "sync"

// workspaceUpdated lists the Go consumers of a freshly applied workspace
// projection (the automation curve and the vocal lanes redraw from it).
//
// They are plain Go calls, not DOM listeners. A listener that the page
// dispatches reaches Go through a JavaScript-to-Go event nested inside the
// dispatching call. Go's js/wasm runtime can lose a nested event when two
// goroutines push one at the same time (see nativeFetch), and the job that
// applies a projection runs while the Studio loops poll. A hook runs in the
// goroutine that applied the projection and never leaves Go.
var workspaceUpdated workspaceUpdatedHooks

type workspaceUpdatedHooks struct {
	mu    sync.Mutex
	hooks []*workspaceUpdatedHook
}

type workspaceUpdatedHook struct {
	fn      func()
	removed bool
}

// add registers fn and returns the function that removes it. Removing twice is
// safe. A hook that is removed while run is in progress is not called again.
func (h *workspaceUpdatedHooks) add(fn func()) (remove func()) {
	hook := &workspaceUpdatedHook{fn: fn}
	h.mu.Lock()
	h.hooks = append(h.hooks, hook)
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		hook.removed = true
		for i, candidate := range h.hooks {
			if candidate == hook {
				h.hooks = append(h.hooks[:i:i], h.hooks[i+1:]...)
				return
			}
		}
	}
}

// run calls the registered hooks in registration order. It works from a copy
// of the list, so a hook may add or remove hooks, and it holds no lock while a
// hook runs.
func (h *workspaceUpdatedHooks) run() {
	h.mu.Lock()
	snapshot := append([]*workspaceUpdatedHook(nil), h.hooks...)
	h.mu.Unlock()
	for _, hook := range snapshot {
		h.mu.Lock()
		removed := hook.removed
		h.mu.Unlock()
		if !removed {
			hook.fn()
		}
	}
}
