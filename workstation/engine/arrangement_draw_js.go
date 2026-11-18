package main

import (
	"math"
	"strconv"
	"strings"
	"syscall/js"
)

func (v *placementView) showArrangeTool() {
	mode := "select"
	if v.drawTool {
		mode = "draw"
	}
	v.root.Call("setAttribute", "data-arrange-tool", mode)
	v.root.Call("querySelector", "[name=arrangeTool]").Set("value", mode)
	picker := v.root.Call("querySelector", "[name=arrangeBrush]")
	picker.Set("value", v.brush)
	if picker.Get("selectedIndex").Int() < 0 {
		v.brush = ""
		picker.Set("value", "")
	}
	picker.Call("closest", "label").Set("hidden", !v.drawTool)
	status := v.root.Call("querySelector", "[data-draw-hint]")
	status.Set("hidden", !v.drawTool)
	status.Set("textContent", workspaceAttr(status, "data-draw-hint"))
	cells := v.root.Call("querySelectorAll", ".placement-lane, [data-draw-scene]:not(a)")
	for i := 0; i < cells.Length(); i++ {
		if v.drawTool {
			cells.Index(i).Set("tabIndex", 0)
		} else {
			cells.Index(i).Call("removeAttribute", "tabindex")
		}
	}
}

func (v *placementView) paintPattern(event, cell js.Value) {
	status := v.root.Call("querySelector", "[data-draw-hint]")
	track, pattern, chosen := strings.Cut(v.brush, ":")
	if !chosen || track != workspaceAttr(cell, "data-draw-track") {
		status.Set("textContent", "Choose a pattern for this track.")
		return
	}
	panel := v.root.Call("closest", "#arrangement")
	form := panel.Call("querySelector", "[data-arrange-write] form")
	if !workspacePresent(form) {
		return
	}
	// A background region read also marks the shell busy. The form handler
	// refreshes a stale revision before it submits the draw.
	if workspacePresent(form.Get("ownerDocument").Call("querySelector", "form[data-gosx-form-state=pending], form[data-gosx-form-state=submitting]")) {
		status.Set("textContent", "Saving the current edit. Draw again when it finishes.")
		return
	}
	if scene := workspaceAttr(cell, "data-draw-scene"); scene != "" {
		workspaceField(form, "scene").Set("value", scene)
		workspaceField(form, "pattern").Set("value", pattern)
	} else {
		if workspacePresent(event.Get("target").Call("closest", "[data-placement-block]")) {
			status.Set("textContent", "That position is occupied. Choose an empty space.")
			return
		}
		option := v.root.Call("querySelector", "[name=arrangeBrush] option:checked")
		length := placementTicks(option, "data-length")
		grid := placementCompatibleStep(cell, false, placementSnap(cell))
		var at int64
		keyboard := event.Get("type").String() == "keydown"
		if !keyboard {
			rect := cell.Call("getBoundingClientRect")
			fraction := (event.Get("clientX").Float() - rect.Get("left").Float()) / rect.Get("width").Float()
			at = int64(math.Round(fraction*float64(placementTicks(cell, "data-placement-ticks"))/float64(grid))) * grid
		}
		// Blocks are in timeline order. End at the next occupied region, and
		// for keyboard entry advance over occupied regions to the first gap.
		blocks := cell.Call("querySelectorAll", "[data-placement-block]")
		for i := 0; i < blocks.Length(); i++ {
			block := blocks.Index(i)
			start := placementTicks(block, "data-placement-at")
			end := start + placementTicks(block, "data-placement-length")
			if start > at {
				length = min(length, start-at)
				break
			}
			if at < end {
				if !keyboard {
					status.Set("textContent", "That position is occupied. Choose an empty space.")
					return
				}
				at = (end + grid - 1) / grid * grid
			}
		}
		if at < 0 || at >= placementTicks(cell, "data-placement-ticks") || length <= 0 {
			status.Set("textContent", "There is no space here. Use Add a placement to extend the timeline.")
			return
		}
		workspaceField(form, "content").Set("value", pattern)
		workspaceField(form, "at").Set("value", strconv.FormatInt(at, 10)+"ticks")
		workspaceField(form, "length").Set("value", strconv.FormatInt(length, 10)+"ticks")
	}
	workspaceField(form, "track").Set("value", track)
	form.Call("requestSubmit")
}
