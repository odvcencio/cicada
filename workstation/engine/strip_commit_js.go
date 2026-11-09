//go:build js && wasm

package main

import (
	"math"
	"strconv"
	"sync"
	"syscall/js"
	"time"
)

// stripSettle is how long a keyboard-driven range waits after its last key
// press before it saves.
const stripSettle = 400 * time.Millisecond

const stripRangeSelector = `input[type="range"][data-commit="settle"]`

// bindStripCommit saves mixer sliders once per gesture. The sliders are plain
// range inputs inside managed forms and carry data-commit="settle"; this
// handler listens on the document, so it keeps working after a re-render.
//
//   - input: the readout (and a pan's aria-valuetext) follows the thumb.
//   - change after a pointer gesture: submit the form at once.
//   - change from the keyboard: each key press restarts a 400 ms timer, and
//     the form is submitted when it fires, or at once if the slider loses
//     focus first. A slider that left the document drops the submit.
func bindStripCommit() func() {
	document := js.Global().Get("document")
	var mu sync.Mutex
	var keyed js.Value // the slider that last received a key press
	var pending js.Value
	var timer *time.Timer
	disposed := false

	target := func(args []js.Value) (js.Value, bool) {
		if len(args) == 0 {
			return js.Undefined(), false
		}
		el := args[0].Get("target")
		if !workspacePresent(el) || el.Get("matches").Type() != js.TypeFunction || !el.Call("matches", stripRangeSelector).Bool() {
			return js.Undefined(), false
		}
		return el, true
	}
	submit := func(el js.Value) {
		if !el.Get("isConnected").Bool() {
			return
		}
		if form := el.Call("closest", "form"); workspacePresent(form) {
			form.Call("requestSubmit")
		}
	}
	// stop cancels a pending keyboard save and reports the slider it was for.
	stop := func() (js.Value, bool) {
		if timer == nil {
			return js.Undefined(), false
		}
		timer.Stop()
		timer = nil
		el := pending
		pending = js.Undefined()
		return el, true
	}

	onInput := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if el, ok := target(args); ok {
			showStripValue(el)
		}
		return nil
	})
	onKeydown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if el, ok := target(args); ok {
			mu.Lock()
			keyed = el
			mu.Unlock()
		}
		return nil
	})
	onPointerdown := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if _, ok := target(args); ok {
			mu.Lock()
			keyed = js.Undefined()
			mu.Unlock()
		}
		return nil
	})
	onChange := js.FuncOf(func(_ js.Value, args []js.Value) any {
		el, ok := target(args)
		if !ok {
			return nil
		}
		mu.Lock()
		if disposed {
			mu.Unlock()
			return nil
		}
		stop()
		if !workspacePresent(keyed) || !keyed.Equal(el) {
			mu.Unlock()
			submit(el) // after a pointer gesture
			return nil
		}
		pending = el
		var mine *time.Timer
		mine = time.AfterFunc(stripSettle, func() {
			mu.Lock()
			fire := !disposed && timer == mine
			if fire {
				timer, pending = nil, js.Undefined()
			}
			mu.Unlock()
			if fire {
				submit(el)
			}
		})
		timer = mine
		mu.Unlock()
		return nil
	})
	onFocusout := js.FuncOf(func(_ js.Value, args []js.Value) any {
		el, ok := target(args)
		if !ok {
			return nil
		}
		mu.Lock()
		waiting, was := js.Undefined(), false
		if !disposed {
			waiting, was = stop()
		}
		mu.Unlock()
		if was && waiting.Equal(el) {
			submit(el)
		}
		return nil
	})
	listeners := []struct {
		name string
		fn   js.Func
	}{{"input", onInput}, {"keydown", onKeydown}, {"pointerdown", onPointerdown}, {"change", onChange}, {"focusout", onFocusout}}
	for _, l := range listeners {
		document.Call("addEventListener", l.name, l.fn, true)
	}
	return func() {
		mu.Lock()
		disposed = true
		stop()
		mu.Unlock()
		for _, l := range listeners {
			document.Call("removeEventListener", l.name, l.fn, true)
			l.fn.Release()
		}
	}
}

// showStripValue writes a slider's current value into its readout. The text
// matches the server render: a pan reads "C", "L35" or "R35" with
// "centre", "35% left" or "35% right" as its aria-valuetext; a level reads
// with one decimal.
func showStripValue(input js.Value) {
	value, err := strconv.ParseFloat(input.Get("value").String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	if pan := input.Call("closest", "[data-mixer-pan]"); workspacePresent(pan) {
		percent := int(math.Round(math.Abs(value) * 100))
		text, spoken := "C", "centre"
		switch {
		case percent == 0:
		case value < 0:
			text, spoken = "L"+strconv.Itoa(percent), strconv.Itoa(percent)+"% left"
		default:
			text, spoken = "R"+strconv.Itoa(percent), strconv.Itoa(percent)+"% right"
		}
		if readout := pan.Call("querySelector", ".strip-pan-readout"); workspacePresent(readout) {
			readout.Set("textContent", text)
		}
		input.Call("setAttribute", "aria-valuetext", spoken)
		return
	}
	if fader := input.Call("closest", "[data-mixer-fader]"); workspacePresent(fader) {
		readout := fader.Call("querySelector", ".strip-readout")
		if !workspacePresent(readout) {
			return
		}
		text := strconv.FormatFloat(value, 'f', 1, 64)
		if first := readout.Get("firstChild"); workspacePresent(first) && first.Get("nodeType").Int() == 3 {
			first.Set("nodeValue", text)
		} else {
			readout.Call("insertBefore", js.Global().Get("document").Call("createTextNode", text), first)
		}
	}
}
