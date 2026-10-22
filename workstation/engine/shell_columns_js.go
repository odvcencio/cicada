//go:build js && wasm

package main

import (
	"math"
	"strconv"
	"syscall/js"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// Fit both sidebars together so restoring a layout cannot consume the song.
// Closed rails keep their stored open width; reopening fits it to this window.
func fitShellColumns(host enginewasm.Context, workbench js.Value) error {
	width := math.Floor(workbench.Call("getBoundingClientRect").Get("width").Float())
	if width < 800 || math.IsNaN(width) || math.IsInf(width, 0) {
		return nil
	}
	type pane struct {
		signal                           string
		minimum, maximum, current, value float64
		open, invalid                    bool
		handle                           js.Value
	}
	panes := []pane{
		{signal: "browserWidth", minimum: 160, maximum: 640, current: 280},
		{signal: "codeWidth", minimum: 240, maximum: 720, current: 360},
	}
	selectors := []string{"#studio-browser", "#studio-code"}
	used, minima := 0.0, 0.0
	for i := range panes {
		p := &panes[i]
		region := workbench.Call("querySelector", selectors[i])
		p.open = region.Call("querySelector", "details").Get("open").Bool()
		p.handle = region.Call("querySelector", "[role=separator]")
		stored, err := enginewasm.ReadSignal[float64](host, "$layout."+p.signal)
		p.invalid = err != nil || math.IsNaN(stored) || math.IsInf(stored, 0)
		if !p.invalid {
			p.current = stored
		}
		p.value = max(p.minimum, min(p.maximum, p.current))
		if p.open {
			used += p.value
			minima += p.minimum
		} else {
			used += 36
			minima += 36
		}
	}
	budget := width - 400
	if used > budget {
		scale := max(0, (budget-minima)/(used-minima))
		for i := range panes {
			p := &panes[i]
			if p.open {
				p.value = p.minimum + math.Floor((p.value-p.minimum)*scale/8)*8
			}
		}
	}
	for i := range panes {
		p, other := &panes[i], &panes[1-i]
		otherWidth := 36.0
		if other.open {
			otherWidth = other.value
		}
		maximum := max(p.minimum, min(p.maximum, budget-otherWidth))
		maximum = p.minimum + math.Floor((maximum-p.minimum)/8)*8
		text := strconv.FormatFloat(maximum, 'f', 0, 64)
		for _, attribute := range []string{"data-gosx-drag-max", "aria-valuemax"} {
			if workspaceAttr(p.handle, attribute) != text {
				p.handle.Call("setAttribute", attribute, text)
			}
		}
		if p.open && (p.invalid || p.value != p.current) {
			if err := host.SetSignal("$layout."+p.signal, p.value); err != nil {
				return err
			}
		}
	}
	return nil
}
