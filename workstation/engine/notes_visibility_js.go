//go:build js && wasm

package main

import (
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// notesVisibilityMount numbers mounts of the Quantize control. A soft
// navigation may mount the new panel before it disposes the old one, so only
// the latest mount writes the signal. Go/WASM runs callbacks on one thread.
var notesVisibilityMount int

// mountNotesVisibility publishes $notesVisible: whether the Clip tab, which
// holds the Quantize control, is on screen. notes.quantize (Mod+U) requires
// it, so the chord never edits a pattern the musician cannot see: another
// dock tab, or another region in the narrow layout, hides the Clip tab. An
// IntersectionObserver reports both, because a hidden panel does not render.
// The panel page at "/" has no Clip dock and leaves the signal false. The
// server gives each render fresh props, so a navigation remounts this engine
// instead of reusing it with a context that can no longer write signals.
func mountNotesVisibility(host enginewasm.Context) (enginewasm.Handle, error) {
	notesVisibilityMount++
	id := notesVisibilityMount
	report := js.FuncOf(func(_ js.Value, args []js.Value) any {
		visible := len(args) > 0 && args[0].Length() > 0 && args[0].Index(args[0].Length()-1).Get("isIntersecting").Bool()
		if notesVisibilityMount == id {
			_ = host.SetSignal("$notesVisible", visible)
		}
		return nil
	})
	report.Invoke()
	var watcher js.Value
	panel := host.Mount().Call("closest", "#detail-panel-clip")
	if observer := js.Global().Get("IntersectionObserver"); !panel.IsNull() && observer.Type() == js.TypeFunction {
		watcher = observer.New(report)
		watcher.Call("observe", panel)
	}
	return enginewasm.HandleFunc(func() {
		if !watcher.IsUndefined() {
			watcher.Call("disconnect")
		}
		report.Invoke()
		report.Release()
	}), nil
}
