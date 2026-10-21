//go:build js && wasm

package main

import (
	"math"
	"syscall/js"
)

// Edge scrolling is local to a moved, captured pointer. A short dwell avoids
// panning during an ordinary release near an edge; there is no idle timer.
func (drag *placementDrag) scrollVelocity() (speed, limit float64) {
	scroller := drag.scroller
	box := scroller.Call("getBoundingClientRect")
	left := box.Get("left").Float() + scroller.Get("clientLeft").Float()
	right := left + scroller.Get("clientWidth").Float()
	top, bottom := math.Max(0, box.Get("top").Float()), math.Min(js.Global().Get("innerHeight").Float(), box.Get("bottom").Float())
	if pane := scroller.Call("closest", ".shell-scroll"); workspacePresent(pane) {
		clip := pane.Call("getBoundingClientRect")
		top, bottom = math.Max(top, clip.Get("top").Float()), math.Min(bottom, clip.Get("bottom").Float())
	}
	if drag.clientY < top || drag.clientY > bottom || scroller.Call("getClientRects").Length() == 0 {
		return 0, 0
	}
	world := scroller.Call("querySelector", ".placement-timeline")
	label := world.Call("querySelector", ".placement-track").Call("getBoundingClientRect").Get("width").Float()
	left += label
	if right <= left {
		return 0, 0
	}
	// Absolute previews can overflow the world. Bound scrolling to the authored
	// ruler instead, so moving the last clip cannot create an endless scroll area.
	limit = math.Max(0, world.Get("clientWidth").Float()-scroller.Get("clientWidth").Float())
	zone := math.Min(48, (right-left)/4)
	proximity := 0.
	if drag.clientX < left+zone {
		proximity = -math.Min(1, (left+zone-drag.clientX)/zone)
	} else if drag.clientX > right-zone {
		proximity = math.Min(1, (drag.clientX-(right-zone))/zone)
	}
	speed = 600 * proximity * math.Abs(proximity)
	at := scroller.Get("scrollLeft").Float()
	if (speed < 0 && at <= 0) || (speed > 0 && at >= limit) {
		speed = 0
	}
	return
}

func (u *placementEditorUI) schedulePlacementScroll(drag *placementDrag) {
	if speed, _ := drag.scrollVelocity(); speed == 0 {
		wasScrolling := workspaceAttr(drag.scroller, "data-placement-auto-scroll") != ""
		drag.pauseScroll()
		if wasScrolling {
			u.previewPlacementDrag(drag)
		}
		return
	}
	if drag.scrollFrame != 0 {
		return
	}
	if drag.scrollCallback == nil {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
			drag.scrollFrame = 0
			if u.drag != drag || !drag.block.Get("isConnected").Bool() {
				return nil
			}
			speed, limit := drag.scrollVelocity()
			if speed == 0 {
				wasScrolling := workspaceAttr(drag.scroller, "data-placement-auto-scroll") != ""
				drag.pauseScroll()
				if wasScrolling {
					u.previewPlacementDrag(drag)
				}
				return nil
			}
			now := args[0].Float()
			direction := 1
			if speed < 0 {
				direction = -1
			}
			if drag.edgeAt == 0 || direction != drag.edgeDirection {
				drag.edgeAt, drag.frameAt, drag.edgeDirection = now, now, direction
			}
			elapsed := math.Min(50, math.Max(0, now-drag.frameAt)) / 1000
			drag.frameAt = now
			if now-drag.edgeAt >= 180 {
				before := drag.scroller.Get("scrollLeft").Float()
				drag.scroller.Set("scrollLeft", math.Max(0, math.Min(limit, before+speed*elapsed)))
				if drag.scroller.Get("scrollLeft").Float() == before {
					drag.pauseScroll()
					return nil
				}
				drag.autoScrolled = true
				name := "right"
				if direction < 0 {
					name = "left"
				}
				drag.scroller.Call("setAttribute", "data-placement-auto-scroll", name)
				u.previewPlacementDrag(drag)
			}
			drag.scrollFrame = js.Global().Call("requestAnimationFrame", drag.scrollCallback.Value).Int()
			return nil
		})
		drag.scrollCallback = &callback
	}
	drag.scrollFrame = js.Global().Call("requestAnimationFrame", drag.scrollCallback.Value).Int()
}

func (drag *placementDrag) pauseScroll() {
	if drag.scrollFrame != 0 {
		js.Global().Call("cancelAnimationFrame", drag.scrollFrame)
		drag.scrollFrame = 0
	}
	drag.edgeAt, drag.frameAt, drag.edgeDirection = 0, 0, 0
	drag.scroller.Call("removeAttribute", "data-placement-auto-scroll")
}

func (drag *placementDrag) stopScroll() {
	drag.pauseScroll()
	if drag.scrollCallback != nil {
		drag.scrollCallback.Release()
		drag.scrollCallback = nil
	}
}

func (u *placementEditorUI) abortPlacementDrag(message string) {
	if drag := u.clearPlacementDrag(); drag != nil {
		if drag.autoScrolled {
			drag.scroller.Set("scrollLeft", drag.scroll)
		}
		u.dragClick = true
		u.status(message)
	}
}

func (u *placementEditorUI) placementViewportChanged(event js.Value) {
	switch event.Get("type").String() {
	case "visibilitychange":
		if js.Global().Get("document").Get("hidden").Bool() {
			u.abortPlacementDrag("Placement edit cancelled while the window was hidden.")
		}
	case "blur", "resize":
		if event.Get("target").Equal(js.Global()) {
			u.abortPlacementDrag("Placement edit cancelled after the window changed.")
		}
	}
}
