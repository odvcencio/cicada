//go:build js && wasm

package main

import (
	"encoding/json"
	"math"
	"strconv"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// A restored pixel height is a preference, constrained by the current window.
// Keep the signal and drag bounds at that visible size so the first pointer or
// keyboard move responds immediately after a window resize.
func bindShellDockFit(host enginewasm.Context, root js.Value) (func(), error) {
	window := js.Global()
	workbench := root.Call("querySelector", "#studio-workbench")
	top := workbench.Call("querySelector", ".gsx-dock__top")
	handle := workbench.Call("querySelector", "[aria-label='Resize Detail dock']")
	disposed, queued := false, false
	frame := 0
	fit := func() error {
		if disposed || !root.Get("isConnected").Bool() || window.Get("innerWidth").Float() <= 480 {
			return nil // Phone regions fill the window; preserve the desktop preference.
		}
		height := workbench.Call("getBoundingClientRect").Get("height").Float()
		if height <= 0 || math.IsNaN(height) || math.IsInf(height, 0) {
			return nil
		}
		topHeight := top.Call("getBoundingClientRect").Get("height").Float()
		available := math.Floor(max(0, height-topHeight))
		minimum := min(120.0, available)
		center := min(200.0, max(0, available-minimum))
		maximum := min(640.0, available-center)
		// The recipe's keyboard and pointer steps are eight pixels from its minimum.
		maximum = minimum + math.Floor((maximum-minimum)/8)*8
		style := workbench.Get("style")
		style.Call("setProperty", "--studio-center-min", strconv.FormatFloat(center, 'f', 0, 64)+"px")
		style.Call("setProperty", "--studio-dock-min", strconv.FormatFloat(minimum, 'f', 0, 64)+"px")
		for _, bound := range [2]struct {
			name  string
			value float64
		}{{"min", minimum}, {"max", maximum}} {
			text := strconv.FormatFloat(bound.value, 'f', 0, 64)
			for _, name := range [2]string{"data-gosx-drag-" + bound.name, "aria-value" + bound.name} {
				if workspaceAttr(handle, name) != text {
					handle.Call("setAttribute", name, text)
				}
			}
		}
		current, err := enginewasm.ReadSignal[float64](host, "$layout.dockHeight")
		invalid := err != nil || math.IsNaN(current) || math.IsInf(current, 0)
		if invalid {
			current = 240
		}
		value := max(minimum, min(maximum, current))
		if invalid || value != current {
			return host.SetSignal("$layout.dockHeight", value)
		}
		return nil
	}
	frameFn := js.FuncOf(func(js.Value, []js.Value) any {
		queued = false
		if err := fit(); err != nil && !disposed {
			root.Call("setAttribute", "data-shell-layout", "error")
		}
		return nil
	})
	schedule := func() {
		if disposed || queued {
			return
		}
		queued = true
		frame = window.Call("requestAnimationFrame", frameFn).Int()
	}
	releaseSignal, err := host.SubscribeSignal("$layout.dockHeight", func(json.RawMessage) { schedule() })
	if err != nil {
		frameFn.Release()
		return nil, err
	}
	changed := js.FuncOf(func(js.Value, []js.Value) any { schedule(); return nil })
	observer := js.Undefined()
	if constructor := window.Get("ResizeObserver"); constructor.Type() == js.TypeFunction {
		observer = constructor.New(changed)
		observer.Call("observe", workbench)
		observer.Call("observe", top)
	}
	window.Call("addEventListener", "resize", changed)
	release := func() {
		disposed = true
		releaseSignal()
		window.Call("removeEventListener", "resize", changed)
		if workspacePresent(observer) {
			observer.Call("disconnect")
		}
		if queued {
			window.Call("cancelAnimationFrame", frame)
		}
		changed.Release()
		frameFn.Release()
		root.Call("removeAttribute", "data-shell-layout")
	}
	if err := fit(); err != nil {
		release()
		return nil, err
	}
	root.Call("setAttribute", "data-shell-layout", "ready")
	return release, nil
}
