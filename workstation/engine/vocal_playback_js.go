//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"syscall/js"
	"time"
)

func (u *vocalLanesUI) watchPlayback() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		update := nativePlayback.Get()
		u.playback, u.playbackAt = update.State.Playhead(), update.At
		fresh := update.Ready && time.Since(update.At) < time.Second && u.playback.ClockFresh(time.Since(update.At), time.Second)
		u.playbackClockStale = u.playback.Playing && !fresh
		u.playback.Playing = u.playback.Playing && fresh
		u.streamIssue = update.State.Streaming != nil && (update.State.Streaming.MissingFrames > 0 || update.State.Streaming.Error != "")
		if u.closed.Load() {
			return
		}
		if !js.Global().Get("document").Get("hidden").Bool() && u.root.Call("getClientRects").Length() > 0 {
			u.drawPlayback()
		}
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (u *vocalLanesUI) drawPlayback() {
	line := u.root.Call("querySelector", "[data-vocal-playhead]")
	status := u.root.Call("querySelector", "[data-vocal-playback-status]")
	if !workspacePresent(line) || !workspacePresent(status) {
		return
	}
	line.Set("hidden", !u.playback.Playing)
	if !u.playback.Playing {
		if u.playbackClockStale {
			status.Set("textContent", "Playback position unavailable. Stop and retry playback.")
			return
		}
		status.Set("textContent", "Hear a phrase once, or loop it until you stop. Space on a waveform hears or stops the selection.")
		return
	}
	tick := min(float64(u.props.End), max(0., u.playback.At(time.Since(u.playbackAt))))
	labelWidth := u.world.Call("querySelector", ".vocal-lane-label").Call("getBoundingClientRect").Get("width").Float()
	x := labelWidth + (u.world.Get("scrollWidth").Float()-labelWidth)*tick/float64(u.props.End)
	line.Get("style").Set("left", strconv.FormatFloat(x, 'f', 3, 64)+"px")
	line.Call("setAttribute", "data-playhead-tick", strconv.FormatInt(int64(tick), 10))
	text := "Playing · " + vocalPos(int64(tick)/240*240)
	if r := u.playback.Range; r != nil && r.Name == "Take in song" {
		with := "in song"
		if r.Mode == "solo" {
			with = "vocal only"
		}
		text = "Hearing selected phrase · " + with + " · " + vocalPos(int64(tick)/240*240)
		if r.Loop {
			text = fmt.Sprintf("Looping selected phrase · %s · pass %d · %s", with, r.Cycles+1, vocalPos(int64(tick)/240*240))
		}
	}
	status.Set("textContent", text)
	if u.streamIssue {
		status.Set("textContent", text+" · Audio buffering gaps detected; stop and retry playback.")
	}
	if u.drag != nil || !u.root.Call("querySelector", "[name=vocalFollow]").Get("checked").Bool() {
		return
	}
	// Advance the visible page only near its edge; never recenter every frame.
	width := u.scroll.Get("clientWidth").Float()
	left := u.scroll.Get("scrollLeft").Float()
	if x < left+labelWidth || x > left+width-24 {
		u.scroll.Set("scrollLeft", max(0., x-labelWidth-24))
	}
}
