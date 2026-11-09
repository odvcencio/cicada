//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"syscall/js"
	"time"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

const (
	meterFloor      = -60.0 // dBFS at the bottom of a strip meter
	meterCeiling    = 6.0   // dBFS at the top
	meterHoldFor    = time.Second
	meterHoldFall   = 20.0 // dB per second after the hold
	meterClipFor    = 3 * time.Second
	meterLabelEvery = 500 * time.Millisecond
)

// stripHold is the peak-hold tick and clip latch of one meter.
type stripHold struct {
	at, clipUntil, last time.Time
	db                  float64
}

// mountStripMeters updates the strip meters in the shell Mixer from
// /api/meters. It polls 20 times a second while the Mixer view and the
// document are visible, and checks for visibility four times a second
// otherwise. Each meter element ([data-meter]) gets --peak, --rms and --hold
// (0 to 1 over -60 to +6 dBFS) and data-clip. Its aria-label and the master
// loudness text change at most twice a second.
func mountStripMeters() enginewasm.Handle {
	document := js.Global().Get("document")
	ctx, cancel := context.WithCancel(context.Background())
	holds := map[string]*stripHold{}
	var labelled time.Time
	go func() {
		for {
			visible := stripMetersVisible(document)
			wait := 250 * time.Millisecond
			if visible {
				wait = time.Second / 20
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if !visible {
				continue
			}
			var frame meterFrame
			if readNativeJSON(ctx, "/api/meters", 2*time.Second, 64<<10, &frame) != nil || ctx.Err() != nil {
				continue
			}
			paintStripMeters(document, frame, holds, &labelled)
		}
	}()
	var once sync.Once
	return enginewasm.HandleFunc(func() { once.Do(cancel) })
}

// stripMetersVisible reports whether the Mixer view is on screen in a visible
// document. A hidden tab panel or region has no client rects.
func stripMetersVisible(document js.Value) bool {
	if document.Get("hidden").Bool() {
		return false
	}
	panel := document.Call("getElementById", "view-panel-mixer")
	return !panel.IsNull() && panel.Call("getClientRects").Get("length").Int() > 0
}

func meterFraction(db float64) float64 {
	return math.Max(0, math.Min(1, (db-meterFloor)/(meterCeiling-meterFloor)))
}

func finiteDB(db float64) float64 {
	if math.IsNaN(db) || math.IsInf(db, 0) {
		return -96
	}
	return db
}

func (f meterFrame) lookup(id string) pair {
	kind, name, _ := strings.Cut(id, ":")
	var source map[string]pair
	switch kind {
	case "master":
		return f.Master
	case "track":
		source = f.Tracks
	case "bus":
		source = f.Buses
	case "return":
		source = f.Returns
	}
	if found, ok := source[name]; ok {
		return found
	}
	return pair{Peak: -96, RMS: -96}
}

func paintStripMeters(document js.Value, frame meterFrame, holds map[string]*stripHold, labelled *time.Time) {
	now := time.Now()
	announce := now.Sub(*labelled) >= meterLabelEvery
	if announce {
		*labelled = now
	}
	meters := document.Call("querySelectorAll", "[data-meter]")
	for i := range meters.Get("length").Int() {
		element := meters.Index(i)
		id := element.Call("getAttribute", "data-meter").String()
		level := frame.lookup(id)
		peak, rms := finiteDB(level.Peak), finiteDB(level.RMS)
		hold := holds[id]
		if hold == nil {
			hold = &stripHold{db: peak, at: now, last: now}
			holds[id] = hold
		}
		switch {
		case peak >= hold.db:
			hold.db, hold.at = peak, now
		case now.Sub(hold.at) > meterHoldFor:
			hold.db = math.Max(peak, hold.db-meterHoldFall*now.Sub(hold.last).Seconds())
		}
		hold.last = now
		if peak >= 0 {
			hold.clipUntil = now.Add(meterClipFor)
		}
		clipped := now.Before(hold.clipUntil)
		element.Get("style").Set("cssText", fmt.Sprintf("--peak:%.3f;--rms:%.3f;--hold:%.3f", meterFraction(peak), meterFraction(rms), meterFraction(hold.db)))
		element.Call("setAttribute", "data-clip", fmt.Sprint(clipped))
		if announce {
			label := fmt.Sprintf("%s meter, peak %.1f dBFS, RMS %.1f dBFS", element.Call("getAttribute", "data-meter-name").String(), peak, rms)
			if clipped {
				label += ", clipping"
			}
			element.Call("setAttribute", "aria-label", label)
		}
	}
	if !announce {
		return
	}
	loudness := frame.Loudness
	text := func(key string, value *float64) {
		shown := "—"
		if value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) {
			shown = fmt.Sprintf("%.1f", *value)
		}
		if target := document.Call("querySelector", `[data-loudness="`+key+`"]`); !target.IsNull() {
			target.Set("textContent", shown)
		}
	}
	text("momentary", loudness.Momentary)
	text("short_term", loudness.Short)
	text("integrated", loudness.Integrated)
	text("true_peak", loudness.TruePeak)
}
