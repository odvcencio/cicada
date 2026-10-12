//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

func (u *vocalLanesUI) watchPlayback() {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	lastFetch := time.Time{}
	for {
		if time.Since(lastFetch) >= 250*time.Millisecond {
			lastFetch = time.Now()
			req, err := http.NewRequestWithContext(u.ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/transport", nil)
			if err != nil {
				return
			}
			response, err := client.Do(req)
			var state control.PlaybackState
			if err == nil {
				if response.StatusCode == 200 {
					err = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&state)
				} else {
					err = fmt.Errorf("transport unavailable")
				}
				response.Body.Close()
			}
			if u.closed.Load() {
				return
			}
			if err != nil {
				u.playback.Playing = false
			} else {
				u.playback, u.playbackAt = state.Playhead(), time.Now()
				u.streamIssue = state.Streaming != nil && (state.Streaming.MissingFrames > 0 || state.Streaming.Error != "")
			}
		}
		if u.closed.Load() {
			return
		}
		u.drawPlayback()
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
