//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"syscall/js"
	"time"

	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/signal"
)

type pair struct {
	Peak float64 `json:"peak"`
	RMS  float64 `json:"rms"`
}

// All Studio surfaces share this module. A confirmed workspace revision can
// advance live commands without disposing their input leases or held notes.
var confirmedWorkspaceRevision = signal.New("")

// A retained Live take changes its server projection, while the input engine
// keeps its lease and controls mounted. The workspace owns the refresh.
var workspaceRefreshRequested = signal.New(uint64(0))

type meterFrame struct {
	Tracks   map[string]pair `json:"tracks"`
	Master   pair            `json:"master"`
	Loudness struct {
		Momentary  *float64 `json:"momentary"`
		Short      *float64 `json:"short_term"`
		Integrated *float64 `json:"integrated"`
		Range      *float64 `json:"range"`
		TruePeak   *float64 `json:"true_peak"`
		Dropped    uint64   `json:"dropped_blocks"`
	} `json:"loudness"`
}

// The engine owns only this meter mount. Tymbal and Cicada's kernel remain in
// the native service. GoSX owns module boot, registration, remount and disposal.
func main() {
	if err := enginewasm.Register("CicadaAutomationCurve", mountAutomationCurve); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaCollaboration", mountCollaboration); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaWorkspace", mountWorkspace); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaMeters", mountMeters); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaLive", mountLive); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaTakes", mountTakes); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaVocalLanes", mountVocalLanes); err != nil {
		panic(err)
	}
	if err := enginewasm.Register("CicadaSampler", mountSampler); err != nil {
		panic(err)
	}
	select {}
}

func mountMeters(host enginewasm.Context) (enginewasm.Handle, error) {
	mount := host.Mount()
	if mount.IsNull() || mount.IsUndefined() {
		return nil, fmt.Errorf("meter mount is missing")
	}
	document := js.Global().Get("document")
	canvas := document.Call("createElement", "canvas")
	canvas.Set("width", 640)
	canvas.Set("height", 140)
	canvas.Call("setAttribute", "role", "img")
	canvas.Call("setAttribute", "aria-label", "Tymbal output level meters")
	canvas.Get("style").Set("width", "100%")
	canvas.Get("style").Set("maxWidth", "640px")
	canvas.Get("style").Set("height", "140px")
	canvas.Get("style").Set("display", "block")
	readout := document.Call("createElement", "p")
	readout.Set("className", "muted")
	readout.Get("style").Set("whiteSpace", "pre-line")
	readout.Call("setAttribute", "aria-live", "off")
	mount.Call("replaceChildren", canvas, readout)
	graphics := canvas.Call("getContext", "2d")
	if graphics.IsNull() {
		return nil, fmt.Errorf("2D canvas is unavailable")
	}
	state := signal.New(meterFrame{Master: pair{Peak: -96, RMS: -96}})
	ctx, cancel := context.WithCancel(context.Background())
	var lifecycle sync.Mutex
	disposed := false
	lastAnnounced := time.Time{}
	stopWatch := signal.Watch(func() {
		frame := state.Get()
		lifecycle.Lock()
		defer lifecycle.Unlock()
		if disposed {
			return
		}
		keys := make([]string, 0, len(frame.Tracks))
		for key := range frame.Tracks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		// Paint at the displayed width so narrow viewports do not shrink labels.
		// Large native sessions use the panel width; ordinary sessions retain
		// their 640px desktop limit. Every meter keeps at least 64px of space.
		if len(keys) > 16 {
			canvas.Get("style").Set("maxWidth", "none")
		} else {
			canvas.Get("style").Set("maxWidth", "640px")
		}
		pixelWidth := max(1, int(math.Round(canvas.Call("getBoundingClientRect").Get("width").Float())))
		columns := min(len(keys)+1, max(1, min(17, pixelWidth/64)))
		rows := (len(keys) + 1 + columns - 1) / columns
		height := rows * 140
		if canvas.Get("width").Int() != pixelWidth {
			canvas.Set("width", pixelWidth)
		}
		if canvas.Get("height").Int() != height {
			canvas.Set("height", height)
			canvas.Get("style").Set("height", fmt.Sprintf("%dpx", height))
		}
		canvas.Call("setAttribute", "data-meter-track-count", fmt.Sprint(len(keys)))
		canvas.Call("setAttribute", "data-meter-columns", fmt.Sprint(columns))
		canvas.Call("setAttribute", "aria-label", fmt.Sprintf("Output level meters for %d tracks and master", len(keys)))
		graphics.Set("fillStyle", "#0f1411")
		graphics.Call("fillRect", 0, 0, pixelWidth, height)
		width := float64(pixelWidth) / float64(columns)
		for i, key := range append(keys, "MASTER") {
			meter := frame.Tracks[key]
			if i == len(keys) {
				meter = frame.Master
			}
			peak := meter.Peak
			if math.IsNaN(peak) || math.IsInf(peak, 0) {
				peak = -96
			}
			level := math.Max(0, math.Min(1, (peak+60)/60))
			x := float64(i%columns) * width
			y := float64(i/columns) * 140
			graphics.Set("fillStyle", "#29362d")
			graphics.Call("fillRect", x+4, y+8, width-8, 96)
			color := "#88d9bb"
			if peak > -.3 {
				color = "#ff936f"
			}
			graphics.Set("fillStyle", color)
			graphics.Call("fillRect", x+4, y+104-level*96, width-8, level*96)
			graphics.Set("fillStyle", "#a6b5a6")
			graphics.Set("font", "11px system-ui")
			label := key
			if graphics.Call("measureText", label).Get("width").Float() > width-8 {
				runes := []rune(label)
				for len(runes) > 1 {
					runes = runes[:len(runes)-1]
					label = string(runes) + "…"
					if graphics.Call("measureText", label).Get("width").Float() <= width-8 {
						break
					}
				}
			}
			// Omitting fillText's maxWidth avoids compressing long names below
			// the visible 11px font; ellipsis represents their truncated suffix.
			graphics.Call("fillText", label, x+4, y+126)
		}
		if time.Since(lastAnnounced) >= time.Second {
			value := func(v *float64, unit string) string {
				if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
					return "—"
				}
				return fmt.Sprintf("%.1f %s", *v, unit)
			}
			l := frame.Loudness
			readout.Set("textContent", fmt.Sprintf("Master peak %.1f dBFS · RMS %.1f dBFS\nMomentary %s · Short-term %s · Integrated %s\nLoudness range %s · True peak %s · Dropped blocks %d", frame.Master.Peak, frame.Master.RMS, value(l.Momentary, "LUFS"), value(l.Short, "LUFS"), value(l.Integrated, "LUFS"), value(l.Range, "LU"), value(l.TruePeak, "dBTP"), l.Dropped))
			lastAnnounced = time.Now()
		}
	})
	go func() {
		ticker := time.NewTicker(time.Second / 20)
		defer ticker.Stop()
		client := &http.Client{Timeout: 2 * time.Second}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/meters", nil)
			if err != nil {
				return
			}
			response, err := client.Do(request)
			if err != nil {
				continue
			}
			var frame meterFrame
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&frame)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && ctx.Err() == nil {
				state.Set(frame)
			}
		}
	}()
	var once sync.Once
	return enginewasm.HandleFunc(func() {
		once.Do(func() { cancel(); lifecycle.Lock(); disposed = true; lifecycle.Unlock(); stopWatch.Dispose() })
	}), nil
}
