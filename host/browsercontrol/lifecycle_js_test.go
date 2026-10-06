//go:build js && wasm

package browsercontrol

import (
	"errors"
	"syscall/js"
	"testing"
)

func lifecycleTargets(t *testing.T) (window, document, context, midi js.Value) {
	t.Helper()
	global := js.Global()
	oldWindow, oldDocument := global.Get("window"), global.Get("document")
	eventTarget := global.Get("EventTarget")
	window, document, context, midi = eventTarget.New(), eventTarget.New(), eventTarget.New(), eventTarget.New()
	document.Set("hidden", false)
	context.Set("state", "running")
	global.Set("window", window)
	global.Set("document", document)
	t.Cleanup(func() { global.Set("window", oldWindow); global.Set("document", oldDocument) })
	return
}

func TestLifecycleCloseFailureReentryIsIdempotent(t *testing.T) {
	_, _, context, midi := lifecycleTargets(t)
	c, _ := recordingController(t)
	stops, reports := 0, 0
	closedBeforeReport := false
	c.send = func([]byte) error { stops++; return errors.New("closed worklet port") }
	var lifecycle *Lifecycle
	lifecycle = BindLifecycle(c, context, midi, func(error) {
		reports++
		closedBeforeReport = lifecycle.closed
		// A host error handler may dispose the failing node. Bound the reproduction
		// to one recursive call so the old implementation fails instead of looping.
		if reports == 1 {
			lifecycle.Close()
		}
	})
	lifecycle.Close()
	lifecycle.Close()
	if stops != 1 || reports != 1 || !closedBeforeReport || len(lifecycle.listeners) != 0 {
		t.Fatalf("Close re-entered failure teardown: stops=%d reports=%d closedBeforeReport=%v listeners=%d", stops, reports, closedBeforeReport, len(lifecycle.listeners))
	}
}

func TestLifecycleCloseDetachesBeforeFailureCallback(t *testing.T) {
	window, document, context, midi := lifecycleTargets(t)
	c, _ := recordingController(t)
	stops, reports := 0, 0
	c.send = func([]byte) error { stops++; return errors.New("closed worklet port") }
	lifecycle := BindLifecycle(c, context, midi, func(error) {
		reports++
		if reports == 1 {
			// Host cleanup can synchronously emit the same events that normally Panic.
			window.Call("dispatchEvent", js.Global().Get("Event").New("blur"))
			document.Set("hidden", true)
			document.Call("dispatchEvent", js.Global().Get("Event").New("visibilitychange"))
			context.Set("state", "suspended")
			context.Call("dispatchEvent", js.Global().Get("Event").New("statechange"))
			event := js.Global().Get("Event").New("statechange")
			port := js.Global().Get("Object").New()
			port.Set("type", "input")
			port.Set("state", "disconnected")
			event.Set("port", port)
			midi.Call("dispatchEvent", event)
		}
	})
	lifecycle.Close()
	if stops != 1 || reports != 1 || len(lifecycle.listeners) != 0 {
		t.Fatalf("closing lifecycle received failure-cleanup events: stops=%d reports=%d listeners=%d", stops, reports, len(lifecycle.listeners))
	}
}
