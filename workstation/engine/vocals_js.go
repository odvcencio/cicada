//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
	"m31labs.dev/cicada/workstation/control"
	enginewasm "m31labs.dev/gosx/engine/wasm"
)

type vocalLaneView struct {
	ID, Label         string
	Start, End, Tempo int64
	TempoChanges      []musictime.TempoChange
}
type vocalLaneProps struct {
	End, Start, Stop int64
	Selected, Track  string
	Lanes            []vocalLaneView
	Revision         string
	Phrases          []control.VocalPhrase
	GainDB           float64
	AuditionMode     string
	Edits            control.VocalEdits
}
type vocalPeaks struct {
	Take  string       `json:"take"`
	Start int64        `json:"startTick"`
	End   int64        `json:"endTick"`
	Peaks [][2]float32 `json:"peaks"`
	Peak  float64      `json:"peak"`
}
type vocalPreference struct {
	Zoom, Snap, Selected string
	Follow               bool
	Range                control.VocalRange
	Scroll               float64
	GainDB               float64
	GainRevision         string
	AuditionMode         string
	Edits                *control.VocalEdits
}
type vocalBinding struct {
	node  js.Value
	event string
	fn    js.Func
}
type vocalDrag struct {
	pointer          int
	node             js.Value
	mode, previousID string
	anchor           int64
	before, previous control.VocalRange
	previousGain     float64
	previousEdits    control.VocalEdits
}
type vocalLanesUI struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	closed              atomic.Bool
	root, scroll, world js.Value
	props               vocalLaneProps
	selected            string
	region              control.VocalRange
	bindings            []vocalBinding
	drag                *vocalDrag
	resize              js.Value
	resizeFn            js.Func
	writing             bool
	playback            control.Playhead
	playbackAt          time.Time
	playbackClockStale  bool
	gainDB              float64
	auditionMode        string
	edits               control.VocalEdits
	streamIssue         bool
}

var vocalPeaksCache struct {
	sync.Mutex
	values map[string]vocalPeaks
	order  []string
}

func mountVocalLanes(host enginewasm.Context) (enginewasm.Handle, error) {
	var props vocalLaneProps
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	root := host.Mount()
	if !workspacePresent(root) || props.End <= 0 {
		return nil, fmt.Errorf("vocal lanes are unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &vocalLanesUI{ctx: ctx, cancel: cancel, root: root, props: props, selected: props.Selected, region: control.VocalRange{Start: props.Start, End: props.Stop}, gainDB: props.GainDB, auditionMode: props.AuditionMode, edits: props.Edits}
	u.scroll = root.Call("querySelector", "[data-vocal-scroll]")
	u.world = root.Call("querySelector", "[data-vocal-world]")
	pref := u.preference()
	for name, value := range map[string]string{"vocalZoom": pref.Zoom, "vocalSnap": pref.Snap} {
		field := root.Call("querySelector", "select[name="+name+"]")
		for i := 0; i < field.Get("options").Length(); i++ {
			if field.Get("options").Index(i).Get("value").String() == value {
				field.Set("value", value)
			}
		}
	}
	if lane, ok := u.lane(pref.Selected); ok && pref.Range.Start >= lane.Start && pref.Range.End <= min(lane.End, props.End) && pref.Range.Start < pref.Range.End {
		u.selected, u.region = pref.Selected, pref.Range
		u.gainDB = control.VocalPhraseGain(props.Phrases, u.selected, u.region)
		u.edits = control.VocalPhraseEdits(props.Phrases, u.selected, u.region)
	}
	if pref.GainRevision == props.Revision && control.ValidVocalGain(pref.GainDB) {
		u.gainDB = pref.GainDB
		if pref.Edits != nil {
			u.edits = *pref.Edits
		}
	}
	if pref.AuditionMode == "solo" || pref.AuditionMode == "context" {
		u.auditionMode = pref.AuditionMode
	}
	u.root.Call("querySelector", "[name=vocalFollow]").Set("checked", pref.Follow)
	u.renderSelection(false)
	u.zoom(false)
	u.scroll.Set("scrollLeft", pref.Scroll)
	u.listen(root, "pointerdown", u.pointerDown)
	releaseUpdated := workspaceUpdated.add(func() {
		if u.closed.Load() || !u.root.Get("isConnected").Bool() {
			return
		}
		data := u.root.Call("querySelector", "[data-vocal-lane-props]")
		var current vocalLaneProps
		if !workspacePresent(data) || json.Unmarshal([]byte(workspaceAttr(data, "data-vocal-lane-props")), &current) != nil || current.End <= 0 {
			return
		}
		u.cancelDrag()
		changed := u.props.Revision != current.Revision
		u.props = current
		u.scroll = u.root.Call("querySelector", "[data-vocal-scroll]")
		u.world = u.root.Call("querySelector", "[data-vocal-world]")
		pref := u.preference()
		for name, value := range map[string]string{"vocalZoom": pref.Zoom, "vocalSnap": pref.Snap} {
			field := u.root.Call("querySelector", "select[name="+name+"]")
			for i := 0; i < field.Get("options").Length(); i++ {
				if field.Get("options").Index(i).Get("value").String() == value {
					field.Set("value", value)
				}
			}
		}
		if lane, ok := u.lane(u.selected); ok {
			u.region.Start = min(max(lane.Start, u.region.Start), min(lane.End, current.End)-1)
			u.region.End = min(max(u.region.Start+1, u.region.End), min(lane.End, current.End))
		} else {
			u.selected, u.region = current.Selected, control.VocalRange{Start: current.Start, End: current.Stop}
		}
		if changed {
			u.gainDB = control.VocalPhraseGain(current.Phrases, u.selected, u.region)
			u.edits = control.VocalPhraseEdits(current.Phrases, u.selected, u.region)
		}
		u.root.Call("querySelector", "[name=vocalFollow]").Set("checked", pref.Follow)
		u.zoom(false)
		u.scroll.Set("scrollLeft", pref.Scroll)
		u.renderSelection(false)
		u.root.Call("setAttribute", "data-vocal-lanes-ready", "true")
		go u.loadWaveforms()
		u.savePreference()
	})
	u.listen(root, "pointermove", u.pointerMove)
	u.listen(root, "pointerup", u.pointerUp)
	for _, event := range []string{"pointercancel", "lostpointercapture"} {
		u.listen(root, event, func(e js.Value) {
			if u.drag != nil && e.Get("pointerId").Int() == u.drag.pointer {
				u.cancelDrag()
			}
		})
	}
	u.listen(js.Global(), "blur", func(js.Value) { u.cancelDrag() })
	u.listen(root, "keydown", u.keyDown)
	u.listen(root, "click", u.click)
	u.listen(root, "input", func(e js.Value) {
		target := e.Get("target")
		if name := workspaceAttr(target, "name"); name == "offsetMS" || name == "fadeMS" || name == "crossfadeMS" {
			if n, err := strconv.ParseFloat(target.Get("value").String(), 64); err == nil && !u.busy() && !math.IsNaN(n) && !math.IsInf(n, 0) {
				lo, hi := 0., 100.
				if name == "offsetMS" {
					lo, hi = -500, 500
				}
				if n >= lo && n <= hi {
					switch name {
					case "offsetMS":
						u.edits.OffsetMS = n
					case "fadeMS":
						u.edits.FadeMS = n
					case "crossfadeMS":
						u.edits.CrossfadeMS = n
					}
					u.renderSelection(false)
					u.savePreference()
				}
			}
		}
		if workspaceAttr(target, "name") == "gainDB" || target.Call("hasAttribute", "data-vocal-gain-slider").Bool() {
			if !u.busy() {
				if gain, err := strconv.ParseFloat(target.Get("value").String(), 64); err == nil && control.ValidVocalGain(gain) {
					u.gainDB = gain
					u.renderSelection(false)
					u.savePreference()
				}
			}
		}
		if workspaceAttr(e.Get("target"), "name") == "recordStart" || workspaceAttr(e.Get("target"), "name") == "recordEnd" {
			u.readPositions()
		}
	})
	u.listen(root, "change", func(e js.Value) {
		name := workspaceAttr(e.Get("target"), "name")
		if name == "vocalZoom" {
			u.zoom(true)
			u.savePreference()
		}
		if name == "vocalSnap" || name == "vocalFollow" {
			u.savePreference()
		}
		if name == "auditionMode" {
			u.auditionMode = workspaceFieldValue(u.root, "auditionMode")
			u.savePreference()
		}
	})
	u.listen(u.scroll, "scroll", func(e js.Value) { u.savePreference() })
	if observer := js.Global().Get("ResizeObserver"); observer.Type() == js.TypeFunction {
		u.resizeFn = js.FuncOf(func(js.Value, []js.Value) any {
			if !u.closed.Load() {
				u.zoom(false)
			}
			return nil
		})
		u.resize = observer.New(u.resizeFn)
		u.resize.Call("observe", u.scroll)
	}
	root.Call("setAttribute", "data-vocal-lanes-ready", "true")
	go u.loadWaveforms()
	releasePlayback := playbackPublisher.acquire()
	go u.watchPlayback()
	// Earlier, Later and Reset gain change u.gainDB and u.edits without a form
	// control, so the draft is those values against the accepted ones.
	releaseUnsaved := holdUnsavedWork(fmt.Sprintf("vocal-lanes:%p", u), func() bool {
		return !u.closed.Load() && vocalDraftDiffers(u.gainDB, u.edits, u.props.Phrases, u.selected, u.region)
	})
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		releaseUpdated()
		releaseUnsaved()
		u.cancel()
		releasePlayback()
		u.cancelDrag()
		for _, b := range u.bindings {
			b.node.Call("removeEventListener", b.event, b.fn)
			b.fn.Release()
		}
		if workspacePresent(u.resize) {
			u.resize.Call("disconnect")
			u.resizeFn.Release()
		}
	}), nil
}

func (u *vocalLanesUI) listen(node js.Value, event string, fn func(js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed.Load() && len(args) > 0 {
			fn(args[0])
		}
		return nil
	})
	node.Call("addEventListener", event, f)
	u.bindings = append(u.bindings, vocalBinding{node, event, f})
}
func (u *vocalLanesUI) lane(id string) (vocalLaneView, bool) {
	for _, lane := range u.props.Lanes {
		if lane.ID == id {
			return lane, true
		}
	}
	return vocalLaneView{}, false
}
func vocalPos(tick int64) string {
	if tick%seq.TicksPerStep != 0 {
		return strconv.FormatInt(tick, 10) + "ticks"
	}
	return fmt.Sprintf("@%d.%d.%d", tick/seq.TicksPerBar+1, tick%seq.TicksPerBar/seq.PPQ+1, tick%seq.PPQ/seq.TicksPerStep+1)
}
func (u *vocalLanesUI) grid() int64 {
	value := u.root.Call("querySelector", "select[name=vocalSnap]").Get("value").String()
	switch value {
	case "beat":
		return seq.PPQ
	case "bar":
		return seq.TicksPerBar
	case "free":
		return 1
	default:
		return seq.TicksPerStep
	}
}
func (u *vocalLanesUI) busy() bool {
	capture := u.root.Call("closest", "#cicada-vocals")
	workspace := js.Global().Get("document").Call("getElementById", "cicada-workspace")
	return workspaceAttr(capture, "data-capture-active") == "true" || workspaceAttr(workspace, "aria-busy") == "true"
}
func (u *vocalLanesUI) pick(id string, r control.VocalRange) {
	lane, ok := u.lane(id)
	if !ok {
		return
	}
	hi := min(lane.End, u.props.End)
	if r.End <= r.Start {
		if id == u.selected {
			r = u.region
		} else {
			r = control.VocalRange{Start: lane.Start, End: hi}
		}
	}
	r.Start = max(lane.Start, min(hi-1, r.Start))
	r.End = min(hi, max(r.Start+1, r.End))
	if id != u.selected {
		u.gainDB = control.VocalPhraseGain(u.props.Phrases, id, r)
		u.edits = control.VocalPhraseEdits(u.props.Phrases, id, r)
	}
	u.selected, u.region = id, r
	u.renderSelection(true)
	u.savePreference()
}
func (u *vocalLanesUI) renderSelection(draft bool) {
	u.writing = true
	defer func() { u.writing = false }()
	lane, ok := u.lane(u.selected)
	if !ok {
		return
	}
	rows := u.root.Call("querySelectorAll", "[data-vocal-take]")
	percent := func(t int64) string {
		return strconv.FormatFloat(100*float64(t)/float64(u.props.End), 'f', 6, 64) + "%"
	}
	for i := 0; i < rows.Length(); i++ {
		row := rows.Index(i)
		selected := workspaceAttr(row, "data-vocal-take") == u.selected
		row.Call("setAttribute", "data-vocal-selected", fmt.Sprint(selected))
		area := row.Call("querySelector", "[data-vocal-selection]")
		area.Set("hidden", !selected)
		if !selected {
			continue
		}
		area.Get("style").Set("left", percent(u.region.Start))
		area.Get("style").Set("width", percent(u.region.End-u.region.Start))
		area.Call("setAttribute", "data-start", strconv.FormatInt(u.region.Start, 10))
		area.Call("setAttribute", "data-end", strconv.FormatInt(u.region.End, 10))
		edges := area.Call("querySelectorAll", "[data-vocal-edge]")
		for j := 0; j < edges.Length(); j++ {
			edge := edges.Index(j)
			start := workspaceAttr(edge, "data-vocal-edge") == "start"
			value, lo, hi := u.region.End, u.region.Start+1, min(lane.End, u.props.End)
			if start {
				value, lo, hi = u.region.Start, lane.Start, u.region.End-1
			}
			for k, v := range map[string]string{"aria-valuenow": strconv.FormatInt(value, 10), "aria-valuemin": strconv.FormatInt(lo, 10), "aria-valuemax": strconv.FormatInt(hi, 10), "aria-valuetext": vocalPos(value), "aria-orientation": "horizontal"} {
				edge.Call("setAttribute", k, v)
			}
		}
		area.Call("querySelector", "[data-vocal-move]").Set("hidden", area.Call("getBoundingClientRect").Get("width").Float() < 64)
	}
	forms := u.root.Call("querySelectorAll", "form")
	for i := 0; i < forms.Length(); i++ {
		form := forms.Index(i)
		id := form.Call("querySelector", "input[name=takeId]")
		if workspacePresent(id) {
			id.Set("value", u.selected)
		}
		for name, tick := range map[string]int64{"recordStart": u.region.Start, "recordEnd": u.region.End} {
			input := form.Call("querySelector", "input[name="+name+"]")
			if workspacePresent(input) {
				input.Set("value", vocalPos(tick))
				if draft {
					input.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
				}
			}
		}
	}
	seconds := musictime.SecondsBetween(lane.Tempo, lane.TempoChanges, u.region.Start, u.region.End)
	for _, selector := range []string{"input[name=gainDB]", "[data-vocal-gain-slider]"} {
		if input := u.root.Call("querySelector", selector); workspacePresent(input) {
			// Keep the user's decimal entry intact while typing. The slider and
			// readout still update immediately from each valid number.
			if selector == "input[name=gainDB]" && js.Global().Get("document").Get("activeElement").Equal(input) {
				continue
			}
			input.Set("value", strconv.FormatFloat(u.gainDB, 'f', -1, 64))
		}
	}
	if selectMode := u.root.Call("querySelector", "[name=auditionMode]"); workspacePresent(selectMode) {
		selectMode.Set("value", u.auditionMode)
	}
	readout := fmt.Sprintf("%s · %s → %s · %.2f s · %s", lane.Label, vocalPos(u.region.Start), vocalPos(u.region.End), seconds, control.VocalGainText(u.gainDB))
	for name, value := range map[string]float64{"offsetMS": u.edits.OffsetMS, "fadeMS": u.edits.FadeMS, "crossfadeMS": u.edits.CrossfadeMS} {
		if input := workspaceField(u.root, name); workspacePresent(input) && !js.Global().Get("document").Get("activeElement").Equal(input) {
			input.Set("value", strconv.FormatFloat(value, 'f', -1, 64))
		}
	}
	if u.edits.OffsetMS != 0 {
		readout += fmt.Sprintf(" · advance %+.1f ms", u.edits.OffsetMS)
	}
	u.root.Call("querySelector", "[data-vocal-readout]").Set("textContent", readout)
	u.root.Call("setAttribute", "data-vocal-selected", u.selected)
}
func (u *vocalLanesUI) readPositions() {
	if u.writing {
		return
	}
	form := u.root.Call("querySelector", "form:has(input[name=timeline])")
	if !workspacePresent(form) {
		return
	}
	a, e1 := musictime.MusicalTicks(workspaceFieldValue(form, "recordStart"), true)
	b, e2 := musictime.MusicalTicks(workspaceFieldValue(form, "recordEnd"), true)
	lane, ok := u.lane(u.selected)
	if ok && e1 == nil && e2 == nil && a >= lane.Start && b <= min(lane.End, u.props.End) && a < b {
		u.region = control.VocalRange{Start: a, End: b}
		u.renderSelection(false)
		u.savePreference()
	}
}
func (u *vocalLanesUI) tick(node js.Value, x float64) int64 {
	box := node.Call("getBoundingClientRect")
	return int64(math.Round((x - box.Get("left").Float()) / max(1., box.Get("width").Float()) * float64(u.props.End)))
}
func (u *vocalLanesUI) pointerDown(e js.Value) {
	if u.drag != nil || u.busy() || e.Get("button").Int() != 0 {
		return
	}
	target := e.Get("target")
	if target.Get("closest").Type() != js.TypeFunction {
		return
	}
	node := target.Call("closest", "[data-vocal-wave]")
	if !workspacePresent(node) {
		return
	}
	lane, ok := u.lane(workspaceAttr(node, "data-vocal-wave"))
	if !ok {
		return
	}
	e.Call("preventDefault")
	e.Call("stopPropagation")
	mode := "select"
	edge := target.Call("closest", "[data-vocal-edge]")
	if workspacePresent(edge) {
		mode = workspaceAttr(edge, "data-vocal-edge")
	}
	if workspacePresent(target.Call("closest", "[data-vocal-move]")) {
		mode = "move"
	}
	head := u.tick(node, e.Get("clientX").Float())
	d := &vocalDrag{pointer: e.Get("pointerId").Int(), node: node, mode: mode, anchor: head, previousID: u.selected, previous: u.region, previousGain: u.gainDB, previousEdits: u.edits}
	if mode == "select" {
		u.pick(lane.ID, control.SelectVocalRange(head, head, lane.Start, min(lane.End, u.props.End), u.grid()))
	}
	d.before = u.region
	u.drag = d
	node.Call("setPointerCapture", d.pointer)
	if workspacePresent(edge) {
		edge.Call("focus", map[string]any{"preventScroll": true})
	} else {
		node.Call("focus", map[string]any{"preventScroll": true})
	}
}
func (u *vocalLanesUI) pointerMove(e js.Value) {
	d := u.drag
	if d == nil || e.Get("pointerId").Int() != d.pointer {
		return
	}
	e.Call("preventDefault")
	e.Call("stopPropagation")
	lane, _ := u.lane(u.selected)
	head := u.tick(d.node, e.Get("clientX").Float())
	hi := min(lane.End, u.props.End)
	switch d.mode {
	case "start", "end":
		u.region = control.TrimVocalRange(d.before, d.mode == "end", head, lane.Start, hi, u.grid())
	case "move":
		u.region = control.MoveVocalRange(d.before, head-d.anchor, lane.Start, hi, u.grid())
	default:
		u.region = control.SelectVocalRange(d.anchor, head, lane.Start, hi, u.grid())
	}
	u.renderSelection(true)
}
func (u *vocalLanesUI) releaseDrag() {
	if d := u.drag; d != nil {
		u.drag = nil
		if d.node.Call("hasPointerCapture", d.pointer).Bool() {
			d.node.Call("releasePointerCapture", d.pointer)
		}
	}
}
func (u *vocalLanesUI) pointerUp(e js.Value) {
	if u.drag == nil || u.drag.pointer != e.Get("pointerId").Int() {
		return
	}
	u.pointerMove(e)
	u.releaseDrag()
	u.savePreference()
}
func (u *vocalLanesUI) cancelDrag() {
	if d := u.drag; d != nil {
		u.releaseDrag()
		if !u.closed.Load() {
			u.selected, u.region = d.previousID, d.previous
			u.gainDB = d.previousGain
			u.edits = d.previousEdits
			u.renderSelection(true)
			u.savePreference()
		}
	}
}
func (u *vocalLanesUI) click(e js.Value) {
	target := e.Get("target")
	if target.Get("closest").Type() != js.TypeFunction {
		return
	}
	if pick := target.Call("closest", "[data-vocal-pick]"); workspacePresent(pick) {
		if strings.EqualFold(workspaceString(pick, "tagName"), "a") && (e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() || e.Get("shiftKey").Bool() || e.Get("altKey").Bool()) {
			return
		}
		e.Call("preventDefault")
		e.Call("stopPropagation")
		if !u.busy() {
			u.pick(workspaceAttr(pick, "data-vocal-pick"), control.VocalRange{Start: placementTicks(pick, "data-vocal-start"), End: placementTicks(pick, "data-vocal-end")})
			if pick.Call("hasAttribute", "data-vocal-gain-db").Bool() {
				u.edits = control.VocalPhraseEdits(u.props.Phrases, u.selected, u.region)
				if gain, err := strconv.ParseFloat(workspaceAttr(pick, "data-vocal-gain-db"), 64); err == nil && control.ValidVocalGain(gain) {
					u.gainDB = gain
					u.renderSelection(false)
					u.savePreference()
				}
			}
		}
		return
	}
	if workspacePresent(target.Call("closest", "[data-vocal-gain-reset]")) && !u.busy() {
		u.gainDB = 0
		u.renderSelection(false)
		u.savePreference()
	}
	if nudge := target.Call("closest", "[data-vocal-nudge]"); workspacePresent(nudge) && !u.busy() {
		delta, _ := strconv.ParseFloat(workspaceAttr(nudge, "data-vocal-nudge"), 64)
		u.edits.OffsetMS = max(-500., min(500., u.edits.OffsetMS+delta))
		u.renderSelection(false)
		u.savePreference()
	}
	if workspacePresent(target.Call("closest", "[data-vocal-punch]")) && !u.busy() {
		panel := u.root.Call("closest", "#cicada-vocals")
		form := panel.Call("querySelector", ".vocal-record form")
		for name, tick := range map[string]int64{"recordStart": u.region.Start, "recordEnd": u.region.End} {
			input := workspaceField(form, name)
			input.Set("value", vocalPos(tick))
			input.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
		}
		form.Call("scrollIntoView", map[string]any{"block": "center", "behavior": "auto"})
		workspaceField(form, "recordStart").Call("focus", map[string]any{"preventScroll": true})
	}
}
func (u *vocalLanesUI) keyDown(e js.Value) {
	key := e.Get("key").String()
	if key == "Escape" && u.drag != nil {
		e.Call("preventDefault")
		e.Call("stopPropagation")
		u.cancelDrag()
		return
	}
	target := e.Get("target")
	if target.Get("closest").Type() != js.TypeFunction || u.busy() {
		return
	}
	node := target.Call("closest", "[data-vocal-wave]")
	if !workspacePresent(node) {
		return
	}
	lane, _ := u.lane(workspaceAttr(node, "data-vocal-wave"))
	hi := min(lane.End, u.props.End)
	edge := target.Call("closest", "[data-vocal-edge]")
	if !workspacePresent(edge) && key != "ArrowLeft" && key != "ArrowRight" && key != "Home" && key != "End" && key != "Enter" && key != " " {
		return
	}
	step := max(int64(1), u.grid())
	if e.Get("shiftKey").Bool() {
		step *= 4
	}
	r := u.region
	if lane.ID != u.selected {
		r = control.VocalRange{Start: lane.Start, End: hi}
	}
	switch key {
	case "Enter", " ":
		if !workspacePresent(edge) {
			u.pick(lane.ID, r)
			if key == " " && !e.Get("ctrlKey").Bool() && !e.Get("metaKey").Bool() && !e.Get("altKey").Bool() && !e.Get("repeat").Bool() {
				selector := "button[data-capture-control=listen]"
				if u.playback.Playing {
					selector = "[data-vocal-stop]"
				}
				u.root.Call("querySelector", selector).Call("click")
			}
			e.Call("preventDefault")
			e.Call("stopPropagation")
		}
		return
	case "ArrowLeft", "ArrowDown", "ArrowRight", "ArrowUp", "Home", "End":
		end := workspaceAttr(edge, "data-vocal-edge") == "end"
		value := r.Start
		if end {
			value = r.End
		}
		delta := step
		if key == "ArrowLeft" || key == "ArrowDown" {
			delta = -step
		}
		value += delta
		if workspacePresent(edge) {
			if key == "Home" {
				value = lane.Start
				if end {
					value = r.Start + 1
				}
			}
			if key == "End" {
				value = hi
				if !end {
					value = r.End - 1
				}
			}
			r = control.TrimVocalRange(r, end, value, lane.Start, hi, u.grid())
		} else {
			if key == "Home" {
				delta = lane.Start - r.Start
			}
			if key == "End" {
				delta = hi - r.End
			}
			r = control.MoveVocalRange(r, delta, lane.Start, hi, u.grid())
		}
	default:
		return
	}
	e.Call("preventDefault")
	e.Call("stopPropagation")
	u.pick(lane.ID, r)
}
func (u *vocalLanesUI) zoom(center bool) {
	label := u.world.Call("querySelector", ".vocal-lane-label").Call("getBoundingClientRect").Get("width").Float()
	viewport := u.scroll.Get("clientWidth").Float()
	// Engine mounts can briefly be hidden while layout or navigation settles.
	// Let ResizeObserver retry when the scroll surface has a usable width.
	if math.IsNaN(viewport) || math.IsInf(viewport, 0) || math.IsNaN(label) || math.IsInf(label, 0) || label < 0 || viewport <= label {
		return
	}
	width := viewport
	value := u.root.Call("querySelector", "select[name=vocalZoom]").Get("value").String()
	if value != "song" {
		bars, _ := strconv.Atoi(strings.Fields(value)[0])
		if bars > 0 {
			width = max(viewport, label+max(1., viewport-label)*float64(u.props.End)/float64(int64(bars)*seq.TicksPerBar))
		}
	}
	width = min(2000000., width)
	ticks := control.VocalRulerTicks(u.props.End, width-label)
	if len(ticks) == 0 {
		return
	}
	u.world.Get("style").Set("width", fmt.Sprintf("%.2fpx", width))
	u.world.Get("style").Call("setProperty", "--vocal-grid", fmt.Sprintf("%.6f%%", 100*float64(seq.TicksPerBar)/float64(u.props.End)))
	if center {
		u.scroll.Set("scrollLeft", float64(u.region.Start)/float64(u.props.End)*(width-label))
	}
	ruler := u.world.Call("querySelector", "[data-vocal-ruler]")
	ruler.Call("replaceChildren")
	for _, tick := range ticks {
		span := js.Global().Get("document").Call("createElement", "span")
		span.Set("textContent", strconv.FormatInt(tick/seq.TicksPerBar+1, 10))
		span.Get("style").Set("left", fmt.Sprintf("%.6f%%", 100*float64(tick)/float64(u.props.End)))
		ruler.Call("appendChild", span)
	}
	if !u.closed.Load() {
		u.renderSelection(false)
	}
}
func (u *vocalLanesUI) preferenceKey() string {
	return "cicada-vocal-lanes:" + js.Global().Get("location").Get("origin").String() + ":" + u.props.Track
}
func (u *vocalLanesUI) preference() (p vocalPreference) {
	defer func() { _ = recover() }()
	raw := js.Global().Get("sessionStorage").Call("getItem", u.preferenceKey())
	if raw.Type() == js.TypeString {
		_ = json.Unmarshal([]byte(raw.String()), &p)
	}
	return
}
func (u *vocalLanesUI) savePreference() {
	defer func() { _ = recover() }()
	if u.closed.Load() {
		return
	}
	edits := u.edits
	p := vocalPreference{Selected: u.selected, Range: u.region, Zoom: u.root.Call("querySelector", "select[name=vocalZoom]").Get("value").String(), Snap: u.root.Call("querySelector", "select[name=vocalSnap]").Get("value").String(), Scroll: u.scroll.Get("scrollLeft").Float(), Follow: u.root.Call("querySelector", "[name=vocalFollow]").Get("checked").Bool(), GainDB: u.gainDB, GainRevision: u.props.Revision, AuditionMode: u.auditionMode, Edits: &edits}
	data, _ := json.Marshal(p)
	js.Global().Get("sessionStorage").Call("setItem", u.preferenceKey(), string(data))
}

func (u *vocalLanesUI) loadWaveforms() {
	client := &http.Client{Timeout: 30 * time.Second}
	for _, lane := range u.props.Lanes {
		if u.ctx.Err() != nil {
			return
		}
		key := js.Global().Get("location").Get("origin").String() + "/api/takes/" + url.PathEscape(lane.ID) + "/waveform?bins=8192"
		vocalPeaksCache.Lock()
		peaks, found := vocalPeaksCache.values[key]
		vocalPeaksCache.Unlock()
		var err error
		if !found {
			var request *http.Request
			request, err = http.NewRequestWithContext(u.ctx, http.MethodGet, key, nil)
			if err == nil {
				var response *http.Response
				response, err = client.Do(request)
				if response != nil {
					if response.StatusCode != 200 {
						err = fmt.Errorf("waveform unavailable")
					} else {
						err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&peaks)
					}
					response.Body.Close()
				}
			}
			if err == nil && (peaks.Take != lane.ID || len(peaks.Peaks) == 0 || len(peaks.Peaks) > 8192 || peaks.Start != lane.Start || peaks.End != lane.End) {
				err = fmt.Errorf("waveform bounds differ")
			}
			if err == nil {
				vocalPeaksCache.Lock()
				if vocalPeaksCache.values == nil {
					vocalPeaksCache.values = map[string]vocalPeaks{}
				}
				if len(vocalPeaksCache.order) == 16 {
					delete(vocalPeaksCache.values, vocalPeaksCache.order[0])
					vocalPeaksCache.order = vocalPeaksCache.order[1:]
				}
				vocalPeaksCache.values[key] = peaks
				vocalPeaksCache.order = append(vocalPeaksCache.order, key)
				vocalPeaksCache.Unlock()
			}
		}
		if u.closed.Load() || u.ctx.Err() != nil {
			return
		}
		node := u.root.Call("querySelector", `[data-vocal-wave="`+lane.ID+`"]`)
		if !workspacePresent(node) {
			return
		}
		status := node.Call("querySelector", "[data-vocal-wave-status]")
		if err != nil {
			status.Set("textContent", "Waveform unavailable · use Exact positions or reload. Your take stays saved.")
			node.Call("setAttribute", "data-waveform-state", "error")
			continue
		}
		var path strings.Builder
		scale := max(.000001, peaks.Peak)
		for i, p := range peaks.Peaks {
			x := float64(i) * 8192 / float64(len(peaks.Peaks))
			y := 48 - float64(p[1])/scale*42
			if i == 0 {
				fmt.Fprintf(&path, "M%.2f %.2f", x, y)
			} else {
				fmt.Fprintf(&path, "L%.2f %.2f", x, y)
			}
		}
		for i := len(peaks.Peaks) - 1; i >= 0; i-- {
			x := float64(i+1) * 8192 / float64(len(peaks.Peaks))
			fmt.Fprintf(&path, "L%.2f %.2f", x, 48-float64(peaks.Peaks[i][0])/scale*42)
		}
		path.WriteString("Z")
		wavePath := node.Call("querySelector", "[data-vocal-wave-path]")
		// A canonical DOM patch can insert an HTML path under an existing SVG.
		// The editor owns peak geometry, so always create its path in SVG space.
		if wavePath.Get("namespaceURI").String() != "http://www.w3.org/2000/svg" {
			correct := js.Global().Get("document").Call("createElementNS", "http://www.w3.org/2000/svg", "path")
			correct.Call("setAttribute", "data-vocal-wave-path", "")
			wavePath.Get("parentNode").Call("replaceChild", correct, wavePath)
			wavePath = correct
		}
		wavePath.Call("setAttribute", "d", path.String())
		status.Set("textContent", "")
		node.Call("setAttribute", "data-waveform-state", "ready")
	}
}
