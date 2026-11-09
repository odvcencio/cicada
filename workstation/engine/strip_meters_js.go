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
	meterSilent     = -96.0 // dBFS reported for "no signal" and for missing meters
	meterLostAfter  = 3     // failed fetches in a row before the meters read no signal
)

// stripHold is the peak-hold tick and clip latch of one meter.
type stripHold struct {
	at, clipUntil, last time.Time
	db                  float64
	// The element and the values last written to it. A re-render brings a new
	// element, which starts with nothing written.
	element          js.Value
	css, clip, label string
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
	failures := 0
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
			// Master starts silent, so a frame without it does not read 0 dBFS.
			frame := meterFrame{Master: pair{Peak: meterSilent, RMS: meterSilent}}
			if readNativeJSON(ctx, "/api/meters", 2*time.Second, 64<<10, &frame) != nil {
				if ctx.Err() != nil {
					continue
				}
				failures++
				if failures == meterLostAfter {
					// Show silence instead of the last level, and drop the
					// holds and clip lamps. The next good frame paints again.
					clear(holds)
					paintStripMeters(document, meterFrame{Master: pair{Peak: meterSilent, RMS: meterSilent}}, holds, &labelled, true)
				}
				continue
			}
			if ctx.Err() != nil {
				continue
			}
			failures = 0
			paintStripMeters(document, frame, holds, &labelled, false)
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
		return meterSilent
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
	return pair{Peak: meterSilent, RMS: meterSilent}
}

func paintStripMeters(document js.Value, frame meterFrame, holds map[string]*stripHold, labelled *time.Time, force bool) {
	now := time.Now()
	announce := force || now.Sub(*labelled) >= meterLabelEvery
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
		if !hold.element.Equal(element) {
			hold.element, hold.css, hold.clip, hold.label = element, "", "", ""
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
		if css := fmt.Sprintf("--peak:%.3f;--rms:%.3f;--hold:%.3f", meterFraction(peak), meterFraction(rms), meterFraction(hold.db)); css != hold.css {
			element.Get("style").Set("cssText", css)
			hold.css = css
		}
		if clip := fmt.Sprint(clipped); clip != hold.clip {
			element.Call("setAttribute", "data-clip", clip)
			hold.clip = clip
		}
		if announce {
			name := element.Call("getAttribute", "data-meter-name").String()
			label := fmt.Sprintf("%s meter, peak %.1f dBFS, RMS %.1f dBFS", name, peak, rms)
			if peak <= meterSilent {
				label = name + " meter, no signal"
			} else if clipped {
				label += ", clipping"
			}
			if label != hold.label {
				element.Call("setAttribute", "aria-label", label)
				hold.label = label
			}
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
