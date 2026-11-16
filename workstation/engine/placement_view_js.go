//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

var placementViewPublisher = &sharedLoop{start: func(ctx context.Context) { go runPlacementView(ctx) }}

type placementView struct {
	zoom, snap                    string
	follow, copyDrag              bool
	root, scroll, world           js.Value
	width, left                   float64
	stored                        placementViewPreference
	key                           string
	paused, playing, programmatic bool
	expectedLeft, expectedTop     float64
}

// Session preferences keep a working viewport through ordinary form returns
// and Undo. The loopback origin scopes them to this project/window session.
type placementViewPreference struct {
	Zoom   string
	Snap   string
	Copy   bool
	Follow bool
	Scroll float64
}

const placementViewStorage = "cicada-placement-view"

func readPlacementViewPreference(key string) (p placementViewPreference) {
	p.Zoom = "48"
	if key == "cicada-scene-view" {
		p.Zoom = "fit"
	}
	p.Snap = "240"
	defer func() { _ = recover() }()
	raw := js.Global().Get("sessionStorage").Call("getItem", key)
	if raw.Type() == js.TypeString {
		_ = json.Unmarshal([]byte(raw.String()), &p)
	}
	switch p.Zoom {
	case "fit", "48", "96", "192", "384":
	default:
		p.Zoom = "48"
	}
	p.Snap = normalizedPlacementSnap(p.Snap)
	if p.Scroll < 0 || math.IsNaN(p.Scroll) || math.IsInf(p.Scroll, 0) {
		p.Scroll = 0
	}
	return
}
func (v *placementView) remember() {
	defer func() { _ = recover() }()
	if !workspacePresent(v.scroll) || v.scroll.Call("getClientRects").Length() == 0 {
		return
	}
	v.left = v.scroll.Get("scrollLeft").Float()
	p := placementViewPreference{Zoom: v.zoom, Snap: v.snap, Copy: v.copyDrag, Follow: v.follow, Scroll: v.left}
	if p == v.stored {
		return
	}
	v.stored = p
	data, _ := json.Marshal(p)
	js.Global().Get("sessionStorage").Call("setItem", v.key, string(data))
}

func (v *placementView) find() bool {
	root := js.Global().Get("document").Call("querySelector", "[data-placement-navigation], [data-scene-navigation]")
	if !workspacePresent(root) || root.Call("getClientRects").Length() == 0 {
		return false
	}
	changed := !workspacePresent(v.root) || !v.root.Equal(root)
	v.root = root
	key := placementViewStorage
	scrollSelector, worldSelector := ".placement-scroll", ".placement-timeline"
	if root.Call("hasAttribute", "data-scene-navigation").Bool() {
		key = "cicada-scene-view"
		scrollSelector, worldSelector = ".timeline", ".scene-timeline"
	}
	if key != v.key {
		p := readPlacementViewPreference(key)
		v.key, v.zoom, v.snap, v.copyDrag, v.follow, v.left, v.stored = key, p.Zoom, p.Snap, p.Copy, p.Follow, p.Scroll, p
		v.paused, v.playing = false, false
	}
	v.scroll = root.Call("querySelector", scrollSelector)
	v.world = root.Call("querySelector", worldSelector)
	restore := changed || !root.Call("hasAttribute", "data-placement-navigation-ready").Bool()
	if restore {
		root.Call("setAttribute", "data-placement-navigation-ready", "")
	}
	zoom := root.Call("querySelector", "[name=placementZoom]")
	if restore && zoom.Get("value").String() != v.zoom {
		zoom.Set("value", v.zoom)
	}
	follow := root.Call("querySelector", "[name=placementFollow]")
	if restore && follow.Get("checked").Bool() != v.follow {
		follow.Set("checked", v.follow)
	}
	if restore {
		if snap := root.Call("querySelector", "[name=placementSnap]"); workspacePresent(snap) {
			snap.Set("value", v.snap)
		}
		if copy := root.Call("querySelector", "[name=placementCopy]"); workspacePresent(copy) {
			copy.Set("checked", v.copyDrag)
		}
		v.showPause()
	}
	if changed || v.width != v.scroll.Get("clientWidth").Float() || workspaceAttr(v.world, "data-placement-zoom") != v.zoom {
		v.scale(false)
	}
	if restore {
		v.setScroll(v.left)
	}
	return true
}

func (v *placementView) labelWidth() float64 {
	return v.world.Call("querySelector", ".placement-track, .timeline-track").Call("getBoundingClientRect").Get("width").Float()
}

func (v *placementView) bars() float64 {
	return math.Max(1, float64(placementTicks(v.root, "data-placement-bars")))
}

func (v *placementView) scale(preserve bool, anchors ...float64) {
	width := v.scroll.Get("clientWidth").Float()
	label := v.labelWidth()
	old := v.world.Call("getBoundingClientRect").Get("width").Float() - label
	anchor := (width - label) / 2
	if len(anchors) > 0 {
		anchor = anchors[0]
	}
	center := 0.
	if preserve && old > 0 {
		center = (v.scroll.Get("scrollLeft").Float() + anchor) / old
	}
	barWidth, _ := strconv.ParseFloat(v.zoom, 64)
	if v.zoom == "fit" {
		barWidth = math.Max(.000001, (width-label)/v.bars())
	}
	v.world.Get("style").Call("setProperty", "--placement-bar-width", fmt.Sprintf("%.6fpx", barWidth))
	v.world.Call("setAttribute", "data-placement-zoom", v.zoom)
	// Keep bar numbers legible when the entire song is fitted on a phone.
	stride := int64(math.Max(1, math.Ceil(32/barWidth)))
	labels := v.world.Call("querySelectorAll", "[data-placement-bar]")
	for i := 0; i < labels.Length(); i++ {
		node := labels.Index(i)
		node.Set("hidden", (placementTicks(node, "data-placement-bar")-1)%stride != 0)
	}
	v.width = width
	if v.zoom == "fit" {
		v.setScroll(0)
	} else if preserve {
		newWidth := v.world.Call("getBoundingClientRect").Get("width").Float() - v.labelWidth()
		v.setScroll(math.Max(0, center*newWidth-anchor))
	}
}

func (v *placementView) setScroll(left float64) {
	v.programmatic = true
	v.scroll.Set("scrollLeft", left)
	v.expectedLeft = v.scroll.Get("scrollLeft").Float()
	v.expectedTop = v.scroll.Get("scrollTop").Float()
	v.programmatic = false
}

func (v *placementView) showPause() {
	if !workspacePresent(v.root) {
		return
	}
	control := v.root.Call("querySelector", "[name=placementFollow]")
	if !workspacePresent(control) {
		return
	}
	text := "Follow song"
	if v.paused && v.follow {
		control.Call("setAttribute", "data-follow-paused", "")
		text = "Follow (paused)"
	} else {
		control.Call("removeAttribute", "data-follow-paused")
	}
	// The label is optional markup: a page without it must not stop the engine.
	if label := v.root.Call("querySelector", "[data-follow-label]"); workspacePresent(label) {
		label.Set("textContent", text)
	}
}

func (v *placementView) pauseFollow() {
	if v.follow {
		v.paused = true
		v.showPause()
	}
}

func (v *placementView) stopFollow() {
	v.follow = false
	v.paused = false
	v.showPause()
	if workspacePresent(v.root) {
		v.root.Call("querySelector", "[name=placementFollow]").Set("checked", false)
	}
}

func (v *placementView) event(event js.Value) {
	target := event.Get("target")
	if !workspacePresent(target) || target.Get("closest").Type() != js.TypeFunction {
		return
	}
	if event.Get("type").String() == "click" && !workspacePresent(target.Call("closest", "[data-placement-view]")) {
		return
	}
	value, checked := workspaceString(target, "value"), target.Get("checked")
	root := target.Call("closest", "[data-placement-navigation], [data-scene-navigation]")
	if !workspacePresent(root) {
		return
	}
	if (!workspacePresent(v.root) || !root.Equal(v.root)) && !v.find() {
		return
	}
	switch event.Get("type").String() {
	case "input", "change":
		switch workspaceAttr(target, "name") {
		case "placementZoom":
			v.zoom = value
			target.Set("value", value)
			v.scale(true)
		case "placementSnap":
			v.snap = normalizedPlacementSnap(value)
			target.Set("value", v.snap)
		case "placementCopy":
			v.copyDrag = checked.Type() == js.TypeBoolean && checked.Bool()
		case "placementFollow":
			v.follow = checked.Type() == js.TypeBoolean && checked.Bool()
			v.paused = false
			v.showPause()
			target.Set("checked", v.follow)
		}
	case "click":
		button := target.Call("closest", "[data-placement-view]")
		if !workspacePresent(button) {
			return
		}
		switch workspaceAttr(button, "data-placement-view") {
		case "fit":
			v.zoom = "fit"
			v.root.Call("querySelector", "[name=placementZoom]").Set("value", v.zoom)
			v.scale(false)
		case "selected":
			block := root.Call("querySelector", "[data-placement-selected=true]")
			if workspacePresent(block) {
				v.stopFollow()
				ticks := v.bars() * float64(control.GridBarTicks())
				width := v.world.Call("getBoundingClientRect").Get("width").Float() - v.labelWidth()
				v.setScroll(math.Max(0, float64(placementTicks(block, "data-placement-at"))*width/ticks-16))
			}
		}
	case "wheel":
		if workspacePresent(target.Call("closest", ".placement-scroll, .timeline")) {
			if event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool() {
				event.Call("preventDefault")
				options := []string{"fit", "48", "96", "192", "384"}
				index := 0
				for i, option := range options {
					if option == v.zoom {
						index = i
						break
					}
				}
				if event.Get("deltaY").Float() < 0 {
					index = min(len(options)-1, index+1)
				} else {
					index = max(0, index-1)
				}
				v.zoom = options[index]
				v.root.Call("querySelector", "[name=placementZoom]").Set("value", v.zoom)
				rect := v.scroll.Call("getBoundingClientRect")
				anchor := event.Get("clientX").Float() - rect.Get("left").Float() - v.labelWidth()
				v.scale(true, anchor)
			} else {
				v.pauseFollow()
			}
		}
	case "scroll":
		if target.Equal(v.scroll) && !v.programmatic && (math.Abs(v.scroll.Get("scrollLeft").Float()-v.expectedLeft) > 0.5 || math.Abs(v.scroll.Get("scrollTop").Float()-v.expectedTop) > 0.5) {
			v.pauseFollow()
		}

	}
	v.remember()
}

func (v *placementView) draw() {
	if !v.find() {
		return
	}
	defer v.remember()
	update := nativePlayback.Get()
	line := v.root.Call("querySelector", "[data-placement-playhead]")
	head := update.State.Playhead(control.GridMeterMap())
	if update.Ready {
		if head.Playing && !v.playing {
			v.paused = false
			v.showPause()
		}
		v.playing = head.Playing
	}
	ready := update.Ready && time.Since(update.At) < time.Second && head.ClockFresh(time.Since(update.At), time.Second)
	line.Set("hidden", !ready)
	if !ready {
		status := v.root.Call("querySelector", "[data-placement-playback-status]")
		text := "Playback position unavailable"
		if update.At.IsZero() {
			text = "Connecting to playback…"
		}
		if status.Get("textContent").String() != text {
			status.Set("textContent", text)
		}
		return
	}
	ticks := v.bars() * float64(control.GridBarTicks())
	if v.key != placementViewStorage {
		ticks = float64(placementTicks(v.scroll, "data-scene-ticks"))
	}
	if ticks <= 0 {
		return
	}
	tick := math.Min(ticks, math.Max(0, head.At(time.Since(update.At))))
	label := v.labelWidth()
	width := v.world.Call("getBoundingClientRect").Get("width").Float() - label
	x := label + width*tick/ticks
	leftStyle := fmt.Sprintf("%.3fpx", x)
	if line.Get("style").Get("left").String() != leftStyle {
		line.Get("style").Set("left", leftStyle)
	}
	line.Call("setAttribute", "data-playhead-tick", strconv.FormatInt(int64(tick), 10))
	text := fmt.Sprintf("%s · %s", map[bool]string{true: "Playing", false: "Stopped"}[head.Playing], vocalPos(int64(tick)/240*240))
	status := v.root.Call("querySelector", "[data-placement-playback-status]")
	if status.Get("textContent").String() != text {
		status.Set("textContent", text)
	}
	if v.follow && !v.paused && head.Playing {
		left := v.scroll.Get("scrollLeft").Float()
		if x < left+label || x > left+v.width-24 {
			v.setScroll(math.Max(0, x-label-24))
		}
	}
}

func runPlacementView(ctx context.Context) {
	releasePlayback := playbackPublisher.acquire()
	defer releasePlayback()
	v := &placementView{}
	document := js.Global().Get("document")
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if ctx.Err() == nil && len(args) > 0 {
			v.event(args[0])
		}
		return nil
	})
	for _, name := range []string{"click", "input", "change", "pointerdown", "wheel", "scroll"} {
		document.Call("addEventListener", name, callback, map[string]any{"capture": true, "passive": false})
	}
	defer func() {
		for _, name := range []string{"click", "input", "change", "pointerdown", "wheel", "scroll"} {
			document.Call("removeEventListener", name, callback, true)
		}
		callback.Release()
	}()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if !document.Get("hidden").Bool() {
			v.draw()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
