//go:build js && wasm

package browsercontrol

import (
	"fmt"
	"syscall/js"
)

// WorkletSender posts to an already initialized local AudioWorklet port. It
// never fetches an API or opens a socket. The owner handles failed sends by
// disconnecting the node and showing an actionable audio error.
func WorkletSender(port js.Value) func([]byte) error {
	return func(data []byte) (err error) {
		defer func() {
			if failure := recover(); failure != nil {
				err = fmt.Errorf("CICADA-AUDIO: worklet send failed: %v", failure)
			}
		}()
		bytes := js.Global().Get("Uint8Array").New(len(data))
		js.CopyBytesToJS(bytes, data)
		port.Call("postMessage", map[string]any{"t": "c", "bytes": bytes}, []any{bytes.Get("buffer")})
		return nil
	}
}

type listener struct {
	target js.Value
	event  string
	fn     js.Func
}

type Lifecycle struct {
	controller *Controller
	listeners  []listener
	onError    func(error)
	closed     bool
}

// BindLifecycle releases every note and stops transport on focus loss, hidden
// document, context suspension and any MIDI input disconnect. midiAccess may
// be null/undefined; this method does not request MIDI or microphone access.
// Bind only after the app's explicit user gesture initializes browser audio.
func BindLifecycle(controller *Controller, context, midiAccess js.Value, onError func(error)) *Lifecycle {
	l := &Lifecycle{controller: controller, onError: onError}
	l.add(js.Global().Get("window"), "blur", func(js.Value) { l.panic() })
	l.add(js.Global().Get("document"), "visibilitychange", func(js.Value) {
		if js.Global().Get("document").Get("hidden").Bool() {
			l.panic()
		}
	})
	l.add(context, "statechange", func(js.Value) {
		if context.Get("state").String() != "running" {
			l.panic()
		}
	})
	if !midiAccess.IsNull() && !midiAccess.IsUndefined() {
		l.add(midiAccess, "statechange", func(event js.Value) {
			port := event.Get("port")
			if port.Get("type").String() == "input" && port.Get("state").String() == "disconnected" {
				l.panic()
			}
		})
	}
	return l
}

func (l *Lifecycle) add(target js.Value, event string, run func(js.Value)) {
	fn := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !l.closed && len(args) > 0 {
			run(args[0])
		}
		return nil
	})
	target.Call("addEventListener", event, fn)
	l.listeners = append(l.listeners, listener{target: target, event: event, fn: fn})
}

func (l *Lifecycle) panic() {
	if err := l.controller.Panic(); err != nil && l.onError != nil {
		l.onError(err)
	}
}

// Close releases notes and listeners during score replacement or app disposal.
// The caller should disconnect the old node after Close, even after send failure.
func (l *Lifecycle) Close() {
	if l.closed {
		return
	}
	// Error reporting can re-enter Close or dispatch lifecycle events.
	// Complete listener teardown before reporting a failed Stop send.
	l.closed = true
	for _, entry := range l.listeners {
		entry.target.Call("removeEventListener", entry.event, entry.fn)
		entry.fn.Release()
	}
	l.listeners = nil
	l.panic()
}
