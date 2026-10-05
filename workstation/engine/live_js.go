//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/signal"
)

type liveProps struct {
	CSRF          string              `json:"csrf"`
	Revision      string              `json:"revision"`
	BPM           float64             `json:"bpm"`
	TrackPatterns map[string][]string `json:"trackPatterns"`
}
type liveMapping struct {
	Device  string `json:"device"`
	Channel int    `json:"channel"`
	CC      *int   `json:"cc,omitempty"`
	Note    *int   `json:"note,omitempty"`
	Address string `json:"address,omitempty"`
	Action  string `json:"action,omitempty"`
}
type liveHeld struct {
	Track string
	Note  int
}
type liveJob struct {
	Action string
	Values map[string]string
}
type liveEvent struct {
	Target   js.Value
	Name     string
	Callback js.Func
}
type liveUI struct {
	root        js.Value
	props       liveProps
	lease       string
	sequence    uint64
	ctx         context.Context
	cancel      context.CancelFunc
	closed      atomic.Bool
	jobs        chan liveJob
	events      []liveEvent
	status      *signal.Signal[string]
	held        map[string]liveHeld
	counts      map[liveHeld]int
	active      map[string]int
	takes       []control.Recording
	recording   bool
	bar, step   int64
	playing     bool
	sampled     time.Time
	learning    bool
	mappings    []liveMapping
	descriptors map[string]control.Descriptor
	addresses   map[string]string
	midi        js.Value
	midiEvents  []liveEvent
}

func mountLive(host enginewasm.Context) (enginewasm.Handle, error) {
	var props liveProps
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	root := host.Mount()
	if root.IsNull() {
		return nil, fmt.Errorf("live panel is missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &liveUI{root: root, props: props, lease: js.Global().Get("crypto").Call("randomUUID").String(), ctx: ctx, cancel: cancel, jobs: make(chan liveJob, 128), status: signal.New("Press Play, then use the note keys or enable MIDI."), held: map[string]liveHeld{}, counts: map[liveHeld]int{}, active: map[string]int{}, descriptors: map[string]control.Descriptor{}, addresses: map[string]string{}, midi: js.Undefined()}
	u.loadMappings()
	u.loadSettings()
	u.refreshPatterns()
	watch := signal.Watch(func() { root.Call("querySelector", "[data-live-status]").Set("textContent", u.status.Get()) })
	revisionWatch := signal.Watch(func() {
		if revision := confirmedWorkspaceRevision.Get(); revision != "" && !u.closed.Load() {
			u.props.Revision = revision
		}
	})
	u.bindControls()
	u.renderMappings()
	u.restoreTake()
	u.syncControls()
	go u.runCommands()
	go u.poll()
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		u.closeNotes()
		u.saveTake()
		for _, e := range append(u.events, u.midiEvents...) {
			e.Target.Call("removeEventListener", e.Name, e.Callback)
			e.Callback.Release()
		}
		u.cancel()
		watch.Dispose()
		revisionWatch.Dispose()
		// A released lease rejects late note-ons from an unmounted surface.
		sequence := u.sequence + 1
		go func() {
			ctx, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			_, _ = u.post(ctx, "live", map[string]string{"type": "release", "lease": u.lease, "sequence": strconv.FormatUint(sequence, 10)})
		}()
	}), nil
}

func (u *liveUI) on(target js.Value, name string, fn func(js.Value)) {
	f := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed.Load() && len(args) > 0 {
			fn(args[0])
		}
		return nil
	})
	target.Call("addEventListener", name, f)
	u.events = append(u.events, liveEvent{target, name, f})
}
func (u *liveUI) query(selector string) js.Value  { return u.root.Call("querySelector", selector) }
func (u *liveUI) selected(selector string) string { return u.query(selector).Get("value").String() }
func (u *liveUI) queue(action string, values map[string]string) {
	if u.closed.Load() {
		return
	}
	if action == "live" {
		u.sequence++
		values["lease"] = u.lease
		values["sequence"] = strconv.FormatUint(u.sequence, 10)
	}
	select {
	case u.jobs <- liveJob{action, values}:
	default:
		u.status.Set("Live command queue is full. Reopen Live to resume.")
		u.releaseMount()
	}
}
func (u *liveUI) releaseMount() {
	u.sequence++
	seq := u.sequence
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = u.post(ctx, "live", map[string]string{"type": "release", "lease": u.lease, "sequence": strconv.FormatUint(seq, 10)})
	}()
	u.held = map[string]liveHeld{}
	u.counts = map[liveHeld]int{}
}

func (u *liveUI) post(ctx context.Context, action string, values map[string]string) (json.RawMessage, error) {
	values["csrf_token"] = u.props.CSRF
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	origin := js.Global().Get("location").Get("origin").String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/__actions/"+action, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", u.props.CSRF)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result struct {
		OK      bool            `json:"ok"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		if result.Message == "" {
			result.Message = fmt.Sprintf("Live action failed (%d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("%s", result.Message)
	}
	return result.Data, nil
}
func (u *liveUI) runCommands() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case job := <-u.jobs:
			_, err := u.post(u.ctx, job.Action, job.Values)
			if err != nil && !u.closed.Load() {
				u.status.Set(err.Error())
			}
			if err == nil && job.Action == "note-preview" && !u.closed.Load() {
				u.takes = nil
				u.active = map[string]int{}
				u.saveTake()
				u.syncControls()
				u.status.Set("Note take retained for review. Commit it to patterns, or discard it.")
				workspaceRefreshRequested.Set(workspaceRefreshRequested.Get() + 1)
			}
		}
	}
}
func (u *liveUI) poll() {
	client := &http.Client{Timeout: 2 * time.Second}
	origin := js.Global().Get("location").Get("origin").String()
	read := func(path string, out any) error {
		req, e := http.NewRequestWithContext(u.ctx, http.MethodGet, origin+path, nil)
		if e != nil {
			return e
		}
		r, e := client.Do(req)
		if e != nil {
			return e
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("live state unavailable")
		}
		return json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(out)
	}
	var params struct {
		Registry []struct {
			ID string `json:"id"`
			control.Descriptor
		} `json:"registry"`
		Addresses []struct {
			Address string `json:"address"`
			Param   string `json:"param"`
		} `json:"addresses"`
	}
	if read("/api/params", &params) == nil {
		for _, d := range params.Registry {
			u.descriptors[d.ID] = d.Descriptor
		}
		for _, a := range params.Addresses {
			u.addresses[a.Address] = a.Param
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
		var t struct {
			Bar          int64             `json:"bar"`
			Step         int64             `json:"step"`
			Playing      bool              `json:"playing"`
			Scene        string            `json:"scene"`
			PendingScene string            `json:"pendingScene"`
			ActiveSlots  map[string]string `json:"activeSlots"`
			PendingSlots map[string]string `json:"pendingSlots"`
		}
		if read("/api/transport", &t) == nil && !u.closed.Load() {
			u.bar, u.step, u.playing, u.sampled = t.Bar, t.Step, t.Playing, time.Now()
			launch := "Scene: " + t.Scene
			if t.PendingScene != "" {
				launch += " · queued: " + t.PendingScene
			}
			if !t.Playing {
				launch = "Transport stopped."
			}
			// Launch forms receive workspace projections beside the retained
			// performance mount. Resolve their current DOM on each poll.
			panel := u.root.Call("closest", "#live")
			if panel.IsNull() || panel.IsUndefined() {
				continue
			}
			launchStatus := panel.Call("querySelector", "[data-live-launch-status]")
			if !launchStatus.IsNull() && !launchStatus.IsUndefined() {
				launchStatus.Set("textContent", launch)
			}
			pads := panel.Call("querySelectorAll", "[data-live-slot]")
			for i := 0; i < pads.Length(); i++ {
				pad := pads.Index(i)
				track := pad.Call("getAttribute", "data-live-track").String()
				pattern := pad.Call("getAttribute", "data-live-slot").String()
				state := "idle"
				if t.Playing && t.ActiveSlots[track] == pattern {
					state = "playing"
				}
				if t.PendingSlots[track] == pattern {
					state = "queued"
				}
				pad.Call("setAttribute", "data-state", state)
			}
			if !t.Playing {
				u.recording = false
			}
			if !t.Playing && len(u.held) > 0 {
				u.closeNotes()
				u.status.Set("Transport stopped. Held notes released.")
			}
			u.syncControls()
		}
	}
}
func (u *liveUI) tick() int64 {
	return control.Tick(u.bar, u.step, u.props.BPM, float64(time.Since(u.sampled).Milliseconds()), u.playing)
}

func (u *liveUI) noteOn(id, track string, note, velocity int) {
	if track == "" || !u.playing {
		u.status.Set("Select a track and press Play before sending notes.")
		return
	}
	if _, ok := u.held[id]; ok {
		return
	}
	defer u.syncControls()
	h := liveHeld{track, note}
	u.held[id] = h
	u.counts[h]++
	if u.counts[h] == 1 {
		u.queue("live", map[string]string{"type": "note", "track": track, "note": strconv.Itoa(note), "velocity": strconv.Itoa(velocity), "on": "true"})
	}
	if u.recording {
		pattern := u.selected("[data-live-pattern]")
		if track == u.selected("[data-live-drums]") {
			pattern = u.selected("[data-live-drumpattern]")
		}
		if pattern == "" {
			u.status.Set("Choose a pattern on the selected track before recording.")
			return
		}
		i := -1
		total := 0
		for index, t := range u.takes {
			total += len(t.Notes)
			if t.Track == track && t.Pattern == pattern {
				i = index
			}
		}
		if total >= 512 {
			u.recording = false
			u.status.Set("512-note limit reached. Finish this take.")
			return
		}
		if i < 0 {
			i = len(u.takes)
			u.takes = append(u.takes, control.Recording{Track: track, Pattern: pattern})
		}
		tick := u.tick()
		n := len(u.takes[i].Notes)
		u.takes[i].Notes = append(u.takes[i].Notes, control.Note{Tick: tick, EndTick: tick, Note: note, Velocity: velocity})
		u.active[id] = i*1024 + n
		u.saveTake()
		u.status.Set(fmt.Sprintf("Recording · %d notes", total+1))
	}
}
func (u *liveUI) noteOff(id string) {
	h, ok := u.held[id]
	if !ok {
		return
	}
	defer u.syncControls()
	delete(u.held, id)
	u.counts[h]--
	if u.counts[h] <= 0 {
		delete(u.counts, h)
		u.queue("live", map[string]string{"type": "note", "track": h.Track, "note": strconv.Itoa(h.Note), "velocity": "0", "on": "false"})
	}
	if encoded, ok := u.active[id]; ok {
		i, n := encoded/1024, encoded%1024
		if i < len(u.takes) && n < len(u.takes[i].Notes) {
			note := &u.takes[i].Notes[n]
			note.EndTick = max(note.Tick, u.tick())
		}
		delete(u.active, id)
		u.saveTake()
	}
}
func (u *liveUI) closeNotes() {
	for id := range u.held {
		u.noteOff(id)
	}
}

func (u *liveUI) syncControls() {
	if u.closed.Load() {
		return
	}
	u.query("[data-live-control=record]").Set("disabled", !u.playing || u.recording || len(u.takes) > 0)
	u.query("[data-live-control=record]").Call("setAttribute", "aria-pressed", strconv.FormatBool(u.recording))
	u.query("[data-live-control=finish]").Set("disabled", len(u.takes) == 0)
	buttons := u.root.Call("querySelectorAll", "[data-live-note],[data-live-drum]")
	for i := 0; i < buttons.Length(); i++ {
		track, note := u.buttonNote(buttons.Index(i))
		buttons.Index(i).Set("disabled", !u.playing || track == "")
		buttons.Index(i).Call("setAttribute", "aria-pressed", strconv.FormatBool(u.counts[liveHeld{track, note}] > 0))
	}
}

func (u *liveUI) buttonNote(b js.Value) (string, int) {
	track := u.selected("[data-live-acid]")
	attr := "data-live-note"
	if b.Call("hasAttribute", "data-live-drum").Bool() {
		track = u.selected("[data-live-drums]")
		attr = "data-live-drum"
	}
	note, _ := strconv.Atoi(b.Call("getAttribute", attr).String())
	return track, note
}
func (u *liveUI) bindControls() {
	window := js.Global().Get("window")
	keys := map[string]int{"a": 48, "w": 49, "s": 50, "e": 51, "d": 52, "f": 53, "t": 54, "g": 55, "y": 56, "h": 57, "u": 58, "j": 59, "k": 60}
	u.on(window, "keydown", func(e js.Value) {
		if e.Get("repeat").Bool() || e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() || e.Get("altKey").Bool() {
			return
		}
		t := e.Get("target")
		if t.Type() == js.TypeObject && !t.Call("closest", "input,textarea,select,[contenteditable=true]").IsNull() {
			return
		}
		key := strings.ToLower(e.Get("key").String())
		if key == " " || key == "enter" {
			b := t.Call("closest", "[data-live-note],[data-live-drum]")
			if !b.IsNull() {
				e.Call("preventDefault")
				track, note := u.buttonNote(b)
				u.noteOn("key:"+key, track, note, 100)
				return
			}
		}
		if note, ok := keys[key]; ok {
			e.Call("preventDefault")
			u.noteOn("key:"+key, u.selected("[data-live-acid]"), note, 100)
		}
	})
	u.on(window, "keyup", func(e js.Value) { u.noteOff("key:" + strings.ToLower(e.Get("key").String())) })
	u.on(window, "blur", func(js.Value) { u.closeNotes() })
	u.on(window, "pagehide", func(js.Value) {
		u.sequence++
		form := url.Values{"csrf_token": {u.props.CSRF}, "type": {"release"}, "lease": {u.lease}, "sequence": {strconv.FormatUint(u.sequence, 10)}}
		blob := js.Global().Get("Blob").New([]any{form.Encode()}, map[string]any{"type": "application/x-www-form-urlencoded"})
		js.Global().Get("navigator").Call("sendBeacon", "/__actions/live", blob)
		u.closeNotes()
		u.saveTake()
	})
	u.on(js.Global().Get("document"), "visibilitychange", func(js.Value) {
		if js.Global().Get("document").Get("hidden").Bool() {
			u.closeNotes()
		}
	})
	buttons := u.root.Call("querySelectorAll", "[data-live-note],[data-live-drum]")
	for i := 0; i < buttons.Length(); i++ {
		b := buttons.Index(i)
		u.on(b, "pointerdown", func(e js.Value) {
			e.Call("preventDefault")
			b.Call("focus")
			// Pointer capture may be unavailable or already canceled. A DOM
			// exception must not terminate the shared GoSX Go/WASM module.
			func() { defer func() { _ = recover() }(); b.Call("setPointerCapture", e.Get("pointerId")) }()
			track, note := u.buttonNote(b)
			u.noteOn("pointer:"+e.Get("pointerId").String(), track, note, 100)
		})
		u.on(b, "pointerup", func(e js.Value) { u.noteOff("pointer:" + e.Get("pointerId").String()) })
		u.on(b, "pointercancel", func(e js.Value) { u.noteOff("pointer:" + e.Get("pointerId").String()) })
		u.on(b, "lostpointercapture", func(e js.Value) { u.noteOff("pointer:" + e.Get("pointerId").String()) })
	}
	for _, selector := range []string{"[data-live-acid]", "[data-live-drums]", "[data-live-pattern]", "[data-live-drumpattern]", "[data-live-quantize]"} {
		u.on(u.query(selector), "change", func(js.Value) {
			u.closeNotes()
			u.recording = false
			u.refreshPatterns()
			u.syncControls()
			u.saveTake()
			u.saveSettings()
		})
	}
	button := func(name string, fn func()) {
		u.on(u.query("[data-live-control="+name+"]"), "click", func(js.Value) { fn() })
	}
	button("panic", func() { u.closeNotes(); u.recording = false; u.syncControls(); u.status.Set("Notes released.") })
	button("record", func() {
		if !u.playing {
			u.status.Set("Press Play before recording notes.")
			return
		}
		if len(u.takes) > 0 {
			u.status.Set("Finish the retained take before recording another.")
			return
		}
		u.recording = true
		u.syncControls()
		u.status.Set("Recording notes. Finish the take to review it before committing.")
	})
	button("finish", func() {
		u.closeNotes()
		u.recording = false
		if err := control.ValidateRecordings(u.takes); err != nil {
			u.status.Set(err.Error())
			return
		}
		data, _ := json.Marshal(u.takes)
		u.queue("note-preview", map[string]string{"revision": u.props.Revision, "recordings": string(data)})
		u.status.Set("Retaining note take for review…")
	})
	button("midi", u.enableMIDI)
	button("learn", func() {
		u.learning = true
		u.status.Set("Waiting for a MIDI CC or note for " + u.selected("[data-live-target]"))
	})
	button("clear", func() { u.mappings = nil; u.saveMappings(); u.renderMappings(); u.status.Set("MIDI mappings cleared.") })
}

func (u *liveUI) storage() js.Value {
	defer func() { _ = recover() }()
	return js.Global().Get("localStorage")
}
func (u *liveUI) refreshPatterns() {
	document := js.Global().Get("document")
	for _, pair := range [][2]string{{"[data-live-acid]", "[data-live-pattern]"}, {"[data-live-drums]", "[data-live-drumpattern]"}} {
		selectEl := u.query(pair[1])
		selected := selectEl.Get("value").String()
		selectEl.Call("replaceChildren")
		seen := map[string]bool{}
		for _, pattern := range u.props.TrackPatterns[u.selected(pair[0])] {
			if seen[pattern] {
				continue
			}
			seen[pattern] = true
			o := document.Call("createElement", "option")
			o.Set("value", pattern)
			o.Set("textContent", pattern)
			selectEl.Call("append", o)
		}
		if seen[selected] {
			selectEl.Set("value", selected)
		}
		selectEl.Set("disabled", len(seen) == 0)
	}
}
func (u *liveUI) loadSettings() {
	defer func() { _ = recover() }()
	v := u.storage().Call("getItem", "cicada.live.settings.v1")
	if v.Type() != js.TypeString {
		return
	}
	var saved struct {
		Acid     string `json:"acidTrack"`
		Drums    string `json:"drumTrack"`
		Quantize int    `json:"quantize"`
	}
	if json.Unmarshal([]byte(v.String()), &saved) != nil {
		return
	}
	for _, p := range [][2]string{{"[data-live-acid]", saved.Acid}, {"[data-live-drums]", saved.Drums}, {"[data-live-quantize]", strconv.Itoa(saved.Quantize)}} {
		s := u.query(p[0])
		options := s.Get("options")
		for i := 0; i < options.Length(); i++ {
			if options.Index(i).Get("value").String() == p[1] {
				s.Set("value", p[1])
				break
			}
		}
	}
}
func (u *liveUI) saveSettings() {
	defer func() { _ = recover() }()
	q, _ := strconv.Atoi(u.selected("[data-live-quantize]"))
	data, _ := json.Marshal(map[string]any{"acidTrack": u.selected("[data-live-acid]"), "drumTrack": u.selected("[data-live-drums]"), "quantize": q})
	u.storage().Call("setItem", "cicada.live.settings.v1", string(data))
}

func (u *liveUI) loadMappings() {
	defer func() { _ = recover() }()
	v := u.storage().Call("getItem", "cicada.midi.mappings.v1")
	if v.Type() == js.TypeString {
		_ = json.Unmarshal([]byte(v.String()), &u.mappings)
		if len(u.mappings) > 256 {
			u.mappings = u.mappings[:256]
		}
	}
}
func (u *liveUI) saveMappings() {
	defer func() { _ = recover() }()
	data, _ := json.Marshal(u.mappings)
	u.storage().Call("setItem", "cicada.midi.mappings.v1", string(data))
}
func (u *liveUI) saveTake() {
	defer func() { _ = recover() }()
	data, _ := json.Marshal(struct {
		Revision string              `json:"revision"`
		Takes    []control.Recording `json:"takes"`
	}{u.props.Revision, u.takes})
	js.Global().Get("sessionStorage").Call("setItem", "cicada.note-take", string(data))
}
func (u *liveUI) restoreTake() {
	defer func() { _ = recover() }()
	v := js.Global().Get("sessionStorage").Call("getItem", "cicada.note-take")
	if v.Type() != js.TypeString {
		return
	}
	var saved struct {
		Revision string              `json:"revision"`
		Takes    []control.Recording `json:"takes"`
	}
	if json.Unmarshal([]byte(v.String()), &saved) == nil && control.ValidateRecordings(saved.Takes) == nil {
		u.takes = saved.Takes
		u.props.Revision = saved.Revision
		u.status.Set("An unfinished note take was restored. Finish it to review and commit.")
	}
}
func (u *liveUI) renderMappings() {
	list := u.query("[data-live-mappings]")
	list.Call("replaceChildren")
	for _, m := range u.mappings {
		row := js.Global().Get("document").Call("createElement", "li")
		target := m.Address
		if target == "" {
			target = m.Action
		}
		input := "note"
		number := 0
		if m.Note != nil {
			number = *m.Note
		}
		if m.CC != nil {
			input = "CC"
			number = *m.CC
		}
		row.Set("textContent", fmt.Sprintf("%s · channel %d · %s %d → %s", m.Device, m.Channel+1, input, number, target))
		list.Call("append", row)
	}
}

func awaitPromise(ctx context.Context, p js.Value) (js.Value, error) {
	type result struct {
		v js.Value
		e error
	}
	ch := make(chan result, 1)
	ok := js.FuncOf(func(_ js.Value, a []js.Value) any {
		v := js.Undefined()
		if len(a) > 0 {
			v = a[0]
		}
		ch <- result{v: v}
		return nil
	})
	fail := js.FuncOf(func(_ js.Value, a []js.Value) any {
		message := "Browser permission failed"
		if len(a) > 0 {
			message = a[0].Get("message").String()
		}
		ch <- result{e: fmt.Errorf("%s", message)}
		return nil
	})
	p.Call("then", ok).Call("catch", fail)
	// Pending permission prompts retain callbacks until they settle.
	select {
	case r := <-ch:
		ok.Release()
		fail.Release()
		return r.v, r.e
	case <-ctx.Done():
		go func() { <-ch; ok.Release(); fail.Release() }()
		return js.Undefined(), ctx.Err()
	}
}
func (u *liveUI) enableMIDI() {
	n := js.Global().Get("navigator")
	if n.Get("requestMIDIAccess").Type() != js.TypeFunction {
		u.status.Set("Web MIDI is unavailable. Keyboard and pointer notes remain available.")
		return
	}
	if !u.midi.IsUndefined() {
		u.status.Set("MIDI is already enabled.")
		return
	}
	u.status.Set("Requesting MIDI input access…")
	promise := n.Call("requestMIDIAccess", map[string]any{"sysex": false})
	go func() {
		access, err := awaitPromise(u.ctx, promise)
		if err != nil {
			if !u.closed.Load() {
				u.status.Set(err.Error())
			}
			return
		}
		if u.closed.Load() {
			return
		}
		u.midi = access
		u.bindMIDI()
		u.on(access, "statechange", func(js.Value) { u.closeNotes(); u.bindMIDI() })
	}()
}
func (u *liveUI) bindMIDI() {
	for _, e := range u.midiEvents {
		e.Target.Call("removeEventListener", e.Name, e.Callback)
		e.Callback.Release()
	}
	u.midiEvents = nil
	iter := u.midi.Get("inputs").Call("values")
	count := 0
	for {
		next := iter.Call("next")
		if next.Get("done").Bool() {
			break
		}
		input := next.Get("value")
		device := input.Get("name").String()
		if device == "" {
			device = input.Get("id").String()
		}
		count++
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if !u.closed.Load() && len(args) > 0 {
				u.midiMessage(device, args[0].Get("data"))
			}
			return nil
		})
		input.Call("addEventListener", "midimessage", f)
		u.midiEvents = append(u.midiEvents, liveEvent{input, "midimessage", f})
	}
	u.status.Set(fmt.Sprintf("MIDI enabled · %d inputs · no system-exclusive access", count))
}
func (u *liveUI) midiMessage(device string, data js.Value) {
	if data.Length() < 3 {
		return
	}
	status, a, b := data.Index(0).Int(), data.Index(1).Int(), data.Index(2).Int()
	channel, command := status&15, status&240
	cc, on, off := command == 176, command == 144 && b > 0, command == 128 || command == 144 && b == 0
	if !cc && !on && !off {
		return
	}
	if u.learning && (cc || on) {
		target := u.selected("[data-live-target]")
		if cc && !strings.HasPrefix(target, "param:") {
			u.status.Set("Choose a parameter target for a CC input.")
			return
		}
		if on && strings.HasPrefix(target, "param:") {
			u.status.Set("Use a CC input to learn a parameter.")
			return
		}
		m := liveMapping{Device: device, Channel: channel}
		if cc {
			m.CC = &a
			m.Address = strings.TrimPrefix(target, "param:")
		} else {
			m.Note = &a
			m.Action = target
		}
		for i := len(u.mappings) - 1; i >= 0; i-- {
			old := u.mappings[i]
			match := old.Device == device && old.Channel == channel && (cc && old.CC != nil && *old.CC == a || !cc && old.Note != nil && *old.Note == a)
			if match {
				u.mappings = append(u.mappings[:i], u.mappings[i+1:]...)
			}
		}
		if len(u.mappings) >= 256 {
			u.status.Set("256 mappings reached. Clear mappings before adding more.")
			return
		}
		u.mappings = append(u.mappings, m)
		u.learning = false
		u.saveMappings()
		u.renderMappings()
		u.status.Set("MIDI input learned for " + target)
		return
	}
	for _, m := range u.mappings {
		if m.Device != device || m.Channel != channel {
			continue
		}
		if cc && m.CC != nil && *m.CC == a {
			d, ok := u.descriptors[u.addresses[m.Address]]
			if !ok {
				u.status.Set("Mapped parameter is no longer in this score.")
				return
			}
			v, err := control.CCValue(d, b)
			if err != nil {
				u.status.Set(err.Error())
				return
			}
			value := "off"
			if v != nil {
				value = strconv.FormatFloat(*v, 'f', -1, 64)
			}
			u.queue("live", map[string]string{"type": "param", "address": m.Address, "value": value})
			return
		}
		if !cc && m.Note != nil && *m.Note == a {
			if on {
				u.mappedAction(m.Action)
			}
			return
		}
	}
	if cc {
		return
	}
	track := u.selected("[data-live-acid]")
	if channel == 9 {
		track = u.selected("[data-live-drums]")
		if !control.GMDrum(a) {
			return
		}
	}
	id := fmt.Sprintf("midi:%s:%d:%d", device, channel, a)
	if on {
		u.noteOn(id, track, a, b)
	} else if off {
		u.noteOff(id)
	}
}
func (u *liveUI) mappedAction(target string) {
	parts := strings.Split(target, ":")
	values := map[string]string{"revision": u.props.Revision, "quantize": u.selected("[data-live-quantize]")}
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "scene":
		values["action"] = "launch"
		values["scene"] = parts[1]
	case "slot":
		if len(parts) != 3 {
			return
		}
		values["action"] = "slot"
		values["track"], values["pattern"] = parts[1], parts[2]
	case "stop-track":
		values["action"] = "trackStop"
		values["track"] = parts[1]
	case "transport":
		values["action"] = parts[1]
	default:
		return
	}
	u.queue("transport", values)
}
