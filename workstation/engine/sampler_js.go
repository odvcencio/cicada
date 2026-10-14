//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/host/padsampler"
	enginewasm "m31labs.dev/gosx/engine/wasm"
)

type samplerProjection struct {
	Revision     string          `json:"revision"`
	BankRevision string          `json:"bankRevision"`
	Bank         padsampler.Bank `json:"bank"`
	Sounds       []string        `json:"sounds"`
	Tempo        int             `json:"tempo"`
}
type samplerBinding struct {
	node  js.Value
	event string
	fn    js.Func
}
type samplerVoice struct {
	source, gain       js.Value
	pad, choke, hit    int
	mode               string
	volume, start, end float64
}
type samplerUI struct {
	ctx                                  context.Context
	cancel                               context.CancelFunc
	closed                               atomic.Bool
	loadMu                               sync.Mutex
	root, panel, audio, canvas, graphics js.Value
	bindings                             []samplerBinding
	state                                samplerProjection
	buffers                              map[string]js.Value
	voices                               []*samplerVoice
	perf                                 padsampler.Performance
	initialized, recording, posting      bool
	origin                               float64
	recordRevision                       string
	recordTempo                          int
	waveKey                              string
	drag                                 string
}

func mountSampler(host enginewasm.Context) (enginewasm.Handle, error) {
	root := host.Mount()
	constructor := js.Global().Get("AudioContext")
	if constructor.Type() != js.TypeFunction {
		constructor = js.Global().Get("webkitAudioContext")
	}
	if constructor.Type() != js.TypeFunction {
		return nil, fmt.Errorf("browser audio is unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &samplerUI{ctx: ctx, cancel: cancel, root: root, panel: root.Call("closest", "#sampler-panel"), buffers: map[string]js.Value{}}
	u.audio = constructor.New(map[string]any{"sampleRate": padsampler.Rate, "latencyHint": "interactive"})
	u.canvas = root.Call("querySelector", "[data-sampler-wave]")
	u.graphics = u.canvas.Call("getContext", "2d")
	u.listen(root, "pointerdown", func(e js.Value) {
		target := e.Get("target").Call("closest", "[data-sampler-pad]")
		if target.IsNull() {
			return
		}
		e.Call("preventDefault")
		pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
		u.audio.Call("resume")
		u.hit(pad, 1)
		target.Call("setPointerCapture", e.Get("pointerId"))
	})
	u.listen(root, "pointerup", func(e js.Value) {
		target := e.Get("target").Call("closest", "[data-sampler-pad]")
		if target.IsNull() {
			return
		}
		pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
		u.releasePad(pad)
		if target.Call("hasPointerCapture", e.Get("pointerId")).Bool() {
			target.Call("releasePointerCapture", e.Get("pointerId"))
		}
	})
	u.listen(root, "pointercancel", func(e js.Value) {
		target := e.Get("target").Call("closest", "[data-sampler-pad]")
		if !target.IsNull() {
			pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
			u.releasePad(pad)
		}
	})
	u.listen(root, "click", func(e js.Value) {
		target := e.Get("target")
		if edit := target.Call("closest", "[data-sampler-edit]"); !edit.IsNull() {
			// The shell has no workspace refresh consumer; its Edit link is a
			// real /studio URL, so let the browser navigate to it.
			if js.Global().Get("location").Get("pathname").String() == "/studio" {
				return
			}
			e.Call("preventDefault")
			e.Call("stopImmediatePropagation")
			location, _ := url.Parse(workspaceLocation())
			q := location.Query()
			q.Set("pad", edit.Call("getAttribute", "data-sampler-edit").String())
			location.RawQuery = q.Encode()
			js.Global().Get("history").Call("replaceState", js.Null(), "", location.RequestURI())
			workspaceRefreshRequested.Update(func(n uint64) uint64 { return n + 1 })
			return
		}
		if control := target.Call("closest", "[data-sampler-control]"); !control.IsNull() {
			u.audio.Call("resume")
			switch control.Call("getAttribute", "data-sampler-control").String() {
			case "record":
				u.record()
			case "stop":
				u.stop()
			case "play":
				u.play()
			}
			return
		}
		if pad := target.Call("closest", "[data-sampler-pad]"); !pad.IsNull() && e.Get("detail").Int() == 0 {
			u.audio.Call("resume")
			index, _ := strconv.Atoi(pad.Call("getAttribute", "data-sampler-pad").String())
			u.hit(index, 1)
		}
	})
	u.listen(root, "submit", func(e js.Value) {
		if e.Get("target").Call("closest", "[data-sampler-resample]").IsNull() {
			return
		}
		e.Call("preventDefault")
		e.Call("stopImmediatePropagation")
		u.resample(e.Get("target"))
	})
	document := js.Global().Get("document")
	keys := "1234qwerasdfzxcv"
	for _, event := range []string{"keydown", "keyup"} {
		event := event
		u.listen(document, event, func(e js.Value) {
			if !root.Get("isConnected").Bool() {
				return
			}
			target := e.Get("target")
			if !target.Call("closest", "input,textarea,select,[contenteditable=true]").IsNull() || e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() || e.Get("altKey").Bool() {
				return
			}
			key := strings.ToLower(e.Get("key").String())
			code := e.Get("code").String()
			if strings.HasPrefix(code, "Digit") {
				key = strings.TrimPrefix(code, "Digit")
			}
			if strings.HasPrefix(code, "Key") {
				key = strings.ToLower(strings.TrimPrefix(code, "Key"))
			}
			if key == "escape" && event == "keydown" {
				u.stop()
				return
			}
			if len(key) != 1 {
				return
			}
			i := strings.Index(keys, key)
			if i < 0 {
				return
			}
			e.Call("preventDefault")
			e.Call("stopImmediatePropagation")
			if event == "keyup" {
				u.releasePad(i + 1)
			} else if !e.Get("repeat").Bool() {
				u.audio.Call("resume")
				velocity := 1.
				if e.Get("shiftKey").Bool() {
					velocity = .65
				}
				u.hit(i+1, velocity)
			}
		})
	}
	u.listen(js.Global(), "blur", func(js.Value) { u.stop() })
	u.listen(u.canvas, "pointerdown", func(e js.Value) {
		if u.posting {
			return
		}
		form := u.editor()
		if form.IsNull() {
			return
		}
		src, ok := u.selectedSource()
		if !ok {
			return
		}
		box := u.canvas.Call("getBoundingClientRect")
		frame := int64((e.Get("clientX").Float() - box.Get("left").Float()) / box.Get("width").Float() * float64(src.Asset.Frames))
		start, end := u.bound("start"), u.bound("end")
		u.drag = "start"
		if math.Abs(float64(frame-end)) < math.Abs(float64(frame-start)) {
			u.drag = "end"
		}
		u.canvas.Call("setPointerCapture", e.Get("pointerId"))
		e.Call("preventDefault")
		u.moveBoundary(e)
	})
	u.listen(u.canvas, "pointermove", func(e js.Value) {
		if u.drag != "" {
			u.moveBoundary(e)
		}
	})
	for _, event := range []string{"pointerup", "pointercancel"} {
		u.listen(u.canvas, event, func(e js.Value) {
			u.drag = ""
			if u.canvas.Call("hasPointerCapture", e.Get("pointerId")).Bool() {
				u.canvas.Call("releasePointerCapture", e.Get("pointerId"))
			}
		})
	}
	go u.refreshLoop()
	go u.clockLoop()
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		u.stop()
		cancel()
		for _, b := range u.bindings {
			b.node.Call("removeEventListener", b.event, b.fn)
			b.fn.Release()
		}
		u.audio.Call("close")
	}), nil
}
func (u *samplerUI) listen(node js.Value, event string, handler func(js.Value)) {
	fn := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed.Load() && len(args) > 0 {
			handler(args[0])
		}
		return nil
	})
	node.Call("addEventListener", event, fn)
	u.bindings = append(u.bindings, samplerBinding{node, event, fn})
}
func (u *samplerUI) status(text string) {
	if !u.closed.Load() {
		u.root.Call("querySelector", "[data-sampler-status]").Set("textContent", text)
	}
}
func (u *samplerUI) refreshLoop() {
	for {
		if err := u.load(); err != nil && u.ctx.Err() == nil {
			u.status("Could not prepare pads: " + err.Error())
		}
		select {
		case <-u.ctx.Done():
			return
		case <-time.After(600 * time.Millisecond):
		}
	}
}
func (u *samplerUI) load() error {
	u.loadMu.Lock()
	defer u.loadMu.Unlock()
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequestWithContext(u.ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+"/api/sampler", nil)
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("sampler state is unavailable")
	}
	var state samplerProjection
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&state); err != nil {
		return err
	}
	if u.closed.Load() {
		return nil
	}
	changed := state.BankRevision != u.state.BankRevision
	u.state = state
	if len(state.Sounds) != 16 {
		return fmt.Errorf("invalid pad bank")
	}
	if !u.initialized {
		u.perf = state.Bank.Performance
		u.recordRevision = state.Revision
		u.initialized = true
		u.events()
	}
	for _, key := range []string{"revision", "bankRevision"} {
		value := state.Revision
		if key == "bankRevision" {
			value = state.BankRevision
		}
		nodes := u.root.Call("querySelectorAll", "input[name="+key+"]")
		for i := 0; i < nodes.Length(); i++ {
			nodes.Index(i).Set("value", value)
		}
	}
	location, _ := url.Parse(workspaceLocation())
	selected, _ := strconv.Atoi(location.Query().Get("pad"))
	if selected < 1 || selected > 16 {
		selected = 1
	}
	for i := 0; i < 16; i++ {
		u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", i+1)).Call("closest", ".sampler-pad").Call("setAttribute", "data-pad-selected", strconv.FormatBool(i+1 == selected))
	}
	if preview := u.root.Call("querySelector", "audio"); !preview.IsNull() && state.Sounds[selected-1] != "" {
		path := "/media/sampler/" + state.Sounds[selected-1] + ".wav"
		if preview.Call("getAttribute", "src").String() != path {
			preview.Call("setAttribute", "src", path)
		}
	}
	if changed {
		keep := map[string]bool{}
		for _, id := range state.Sounds {
			keep[id] = true
		}
		for _, h := range u.perf.Hits {
			keep[h.Sound] = true
		}
		for id := range u.buffers {
			if !keep[id] {
				delete(u.buffers, id)
			}
		}
		for i, p := range state.Bank.Pads {
			button := u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", i+1))
			button.Call("querySelector", "strong").Set("textContent", p.Name)
			sub := "Empty"
			if p.Source != "" {
				sub = fmt.Sprintf("%+d st · %s", p.Pitch, p.Mode)
			}
			button.Call("querySelector", "small").Set("textContent", sub)
			_, prepared := u.buffers[state.Sounds[i]]
			button.Set("disabled", p.Source == "" || !prepared)
			button.Set("title", p.Name)
		}
	}
	ids := append([]string(nil), state.Sounds...)
	for _, h := range u.perf.Hits {
		ids = append(ids, h.Sound)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := u.buffers[id]; ok {
			continue
		}
		u.root.Call("setAttribute", "data-sampler-ready", "false")
		u.status("Preparing pad sounds…")
		fetch, err := awaitPromise(u.ctx, js.Global().Call("fetch", "/media/sampler/"+id+".wav"))
		if err != nil {
			return err
		}
		if !fetch.Get("ok").Bool() {
			return fmt.Errorf("prepared sound could not be loaded")
		}
		raw, err := awaitPromise(u.ctx, fetch.Call("arrayBuffer"))
		if err != nil {
			return err
		}
		buffer, err := awaitPromise(u.ctx, u.audio.Call("decodeAudioData", raw))
		if err != nil {
			return err
		}
		if u.closed.Load() {
			return nil
		}
		frames := buffer.Get("length").Int()
		for _, b := range u.buffers {
			frames += b.Get("length").Int()
		}
		if frames > padsampler.MaxFrames {
			return fmt.Errorf("pad monitor exceeds 64 MiB; shorten the loaded regions")
		}
		u.buffers[id] = buffer
		for i, current := range state.Sounds {
			if current == id {
				u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", i+1)).Set("disabled", false)
			}
		}
	}
	u.root.Call("setAttribute", "data-sampler-bank-revision", state.BankRevision)
	u.root.Call("setAttribute", "data-sampler-ready", "true")
	if changed && !u.recording {
		u.status("Pads ready · browser audio · record a performance, then resample it into Session.")
	}
	return nil
}
func (u *samplerUI) now() float64 { return u.audio.Get("currentTime").Float() }
func (u *samplerUI) hit(pad int, velocity float64) {
	if pad < 1 || pad > 16 || len(u.state.Sounds) != 16 {
		return
	}
	id := u.state.Sounds[pad-1]
	buffer, ok := u.buffers[id]
	if !ok {
		u.status("This pad is still preparing. Try again when the bank is ready.")
		return
	}
	when := u.now()
	index := -1
	p := u.state.Bank.Pads[pad-1]
	if u.recording {
		frame := max(int64(0), int64(math.Round((when-u.origin)*padsampler.Rate)))
		if u.root.Call("querySelector", "input[name=quantize]").Get("checked").Bool() {
			step := float64(padsampler.Rate) * 15000 / float64(u.recordTempo)
			frame = int64(math.Ceil(float64(frame)/step) * step)
		}
		if frame >= u.perf.Frames || len(u.perf.Hits) >= 512 {
			u.status("Recording is full; stop and resample the performance.")
			return
		}
		when = u.origin + float64(frame)/padsampler.Rate
		index = len(u.perf.Hits)
		u.perf.Hits = append(u.perf.Hits, padsampler.Hit{Sound: id, Pad: pad, Frame: frame, Velocity: velocity, Choke: p.Choke})
		u.events()
	}
	u.startVoice(buffer, pad, p.Choke, p.Mode, velocity, when, index)
}
func (u *samplerUI) startVoice(buffer js.Value, pad, choke int, mode string, velocity, when float64, hit int) *samplerVoice {
	for _, v := range u.voices {
		if v.end > when && (v.pad == pad || (choke > 0 && v.choke == choke)) {
			u.release(v, when)
		}
	}
	source := u.audio.Call("createBufferSource")
	source.Set("buffer", buffer)
	gain := u.audio.Call("createGain")
	gain.Get("gain").Call("setValueAtTime", velocity, when)
	source.Call("connect", gain)
	gain.Call("connect", u.audio.Get("destination"))
	source.Call("start", when)
	v := &samplerVoice{source: source, gain: gain, pad: pad, choke: choke, mode: mode, volume: velocity, start: when, end: when + buffer.Get("duration").Float(), hit: hit}
	u.voices = append(u.voices, v)
	u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad)).Call("setAttribute", "data-playing", "true")
	return v
}
func (u *samplerUI) release(v *samplerVoice, when float64) {
	if when >= v.end {
		return
	}
	if when < v.start {
		v.source.Call("stop", when)
		v.end = when
		if u.recording && v.hit >= 0 && v.hit < len(u.perf.Hits) {
			u.perf.Hits[v.hit].Velocity = 0
		}
		return
	}
	param := v.gain.Get("gain")
	param.Call("cancelScheduledValues", when)
	param.Call("setValueAtTime", v.volume, when)
	end := min(v.end, when+.005)
	param.Call("linearRampToValueAtTime", 0, end)
	v.source.Call("stop", end)
	v.end = end
}
func (u *samplerUI) releasePad(pad int) {
	for _, v := range u.voices {
		if v.pad == pad && v.mode == "gate" && v.end > u.now() {
			when := u.now()
			u.release(v, when)
			if u.recording && v.hit >= 0 && v.hit < len(u.perf.Hits) {
				h := &u.perf.Hits[v.hit]
				h.GateFrames = max(1, int64(math.Round((when-u.origin)*padsampler.Rate))-h.Frame)
			}
		}
	}
}
func (u *samplerUI) record() {
	if u.posting {
		return
	}
	if u.recording {
		u.stop()
		return
	}
	bars, _ := strconv.Atoi(u.root.Call("querySelector", "input[name=recordBars]").Get("value").String())
	if bars < 1 || bars > 16 || u.state.Tempo < 20000 {
		u.status("Choose 1 to 16 record bars.")
		return
	}
	u.stop()
	u.perf = padsampler.Performance{Frames: int64(math.Round(float64(bars) * 240000 * float64(padsampler.Rate) / float64(u.state.Tempo)))}
	if u.perf.Frames > padsampler.MaxFrames {
		u.status("This tempo needs fewer record bars to stay within the sample limit.")
		u.perf = padsampler.Performance{}
		return
	}
	u.origin = u.now() + .1
	u.recordRevision = u.state.Revision
	u.recordTempo = u.state.Tempo
	u.recording = true
	u.events()
	u.status(fmt.Sprintf("Recording %d bars · pads and Shift for quieter hits · Stop pads finishes early.", bars))
}
func (u *samplerUI) stop() {
	when := u.now()
	for _, v := range u.voices {
		if v.end > when {
			u.release(v, when)
			if u.recording && v.mode == "gate" && v.hit >= 0 && v.hit < len(u.perf.Hits) {
				h := &u.perf.Hits[v.hit]
				h.GateFrames = max(1, min(u.perf.Frames-h.Frame, int64(math.Round((when-u.origin)*padsampler.Rate))-h.Frame))
			}
		}
	}
	wasRecording := u.recording
	u.recording = false
	if wasRecording {
		kept := u.perf.Hits[:0]
		for _, h := range u.perf.Hits {
			if h.Velocity > 0 {
				kept = append(kept, h)
			}
		}
		u.perf.Hits = kept
		u.events()
		u.status(fmt.Sprintf("Recorded %d hits · ready to resample.", len(u.perf.Hits)))
	}
}
func (u *samplerUI) play() {
	if u.recording {
		u.stop()
	}
	if len(u.perf.Hits) == 0 {
		u.status("Record pads first, or open a bank with a saved performance.")
		return
	}
	u.stop()
	hits := append([]padsampler.Hit(nil), u.perf.Hits...)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Frame < hits[j].Frame })
	start := u.now() + .06
	for _, h := range hits {
		buffer, ok := u.buffers[h.Sound]
		if !ok {
			u.status("Performance sounds are preparing; try again when the bank is ready.")
			u.stop()
			return
		}
		when := start + float64(h.Frame)/padsampler.Rate
		mode := "oneshot"
		if h.GateFrames > 0 {
			mode = "gate"
		}
		v := u.startVoice(buffer, h.Pad, h.Choke, mode, h.Velocity, when, -1)
		if h.GateFrames > 0 {
			u.release(v, when+float64(h.GateFrames)/padsampler.Rate)
		}
		end := start + float64(u.perf.Frames)/padsampler.Rate
		if v.end > end {
			u.release(v, max(when, end-.005))
		}
	}
	u.status(fmt.Sprintf("Playing %d recorded hits.", len(hits)))
}
func (u *samplerUI) events() {
	if u.closed.Load() {
		return
	}
	strip := u.root.Call("querySelector", "[data-sampler-events]")
	strip.Call("replaceChildren")
	document := js.Global().Get("document")
	for _, h := range u.perf.Hits {
		node := document.Call("createElement", "span")
		node.Set("title", fmt.Sprintf("Pad %d · %.3f s", h.Pad, float64(h.Frame)/padsampler.Rate))
		node.Get("style").Set("left", fmt.Sprintf("%.4f%%", float64(h.Frame)*100/float64(max(1, u.perf.Frames))))
		strip.Call("appendChild", node)
	}
	data, _ := json.Marshal(u.perf)
	field := u.root.Call("querySelector", "input[name=performance]")
	field.Set("value", string(data))
	u.root.Call("setAttribute", "data-sampler-hit-count", strconv.Itoa(len(u.perf.Hits)))
}
func (u *samplerUI) resample(form js.Value) {
	if u.posting {
		return
	}
	u.stop()
	if len(u.perf.Hits) == 0 {
		u.status("Record a pad performance before resampling.")
		return
	}
	u.posting = true
	data, _ := json.Marshal(u.perf)
	values := map[string]string{"action": "resample", "performance": string(data), "revision": u.recordRevision, "bankRevision": u.state.BankRevision, "__cicada_location": workspaceLocation()}
	for _, key := range []string{"csrf_token", "newName", "track", "at", "destination"} {
		values[key] = form.Call("querySelector", "[name="+key+"]").Get("value").String()
	}
	if form.Call("querySelector", "[name=place]").Get("checked").Bool() {
		values["place"] = "on"
	}
	u.status("Resampling the recorded pad sounds…")
	go func() {
		defer func() { u.posting = false }()
		encoded, _ := json.Marshal(values)
		req, _ := http.NewRequestWithContext(u.ctx, http.MethodPost, js.Global().Get("location").Get("origin").String()+"/__actions/sampler", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Cicada-Reactive", "1")
		req.Header.Set("X-CSRF-Token", values["csrf_token"])
		response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			u.status("Resampling failed; your performance is retained. " + err.Error())
			return
		}
		defer response.Body.Close()
		var result struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
		}
		if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil || !result.OK {
			u.status("Resampling failed; your performance is retained. " + result.Message)
			return
		}
		u.status("Resampled · open Session to arrange the new clip.")
		u.recordRevision = ""
		_ = u.load()
		u.recordRevision = u.state.Revision
		workspaceRefreshRequested.Update(func(n uint64) uint64 { return n + 1 })
	}()
}
func (u *samplerUI) editor() js.Value {
	return u.panel.Call("querySelector", "#sampler-pad-editor form")
}
func (u *samplerUI) bound(name string) int64 {
	form := u.editor()
	if form.IsNull() {
		return 0
	}
	n, _ := strconv.ParseInt(form.Call("querySelector", "[name="+name+"]").Get("value").String(), 10, 64)
	return n
}
func (u *samplerUI) selectedSource() (padsampler.Source, bool) {
	form := u.editor()
	if form.IsNull() {
		return padsampler.Source{}, false
	}
	return u.state.Bank.Source(form.Call("querySelector", "[name=source]").Get("value").String())
}
func (u *samplerUI) moveBoundary(e js.Value) {
	src, ok := u.selectedSource()
	if !ok {
		return
	}
	box := u.canvas.Call("getBoundingClientRect")
	frame := int64(math.Round((e.Get("clientX").Float() - box.Get("left").Float()) / box.Get("width").Float() * float64(src.Asset.Frames)))
	if u.drag == "start" {
		frame = max(0, min(frame, u.bound("end")-1))
	} else {
		frame = max(u.bound("start")+1, min(frame, src.Asset.Frames))
	}
	input := u.editor().Call("querySelector", "[name="+u.drag+"]")
	input.Set("value", strconv.FormatInt(frame, 10))
	input.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
	u.draw()
}
func (u *samplerUI) draw() {
	if u.graphics.IsNull() {
		return
	}
	src, ok := u.selectedSource()
	key := fmt.Sprintf("%s:%d:%d", src.ID, u.bound("start"), u.bound("end"))
	if key == u.waveKey {
		return
	}
	u.waveKey = key
	g := u.graphics
	g.Set("fillStyle", "#101c16")
	g.Call("fillRect", 0, 0, 900, 180)
	if !ok {
		return
	}
	start, end := float64(u.bound("start"))*900/float64(src.Asset.Frames), float64(u.bound("end"))*900/float64(src.Asset.Frames)
	g.Set("fillStyle", "#2f4d37")
	g.Call("fillRect", start, 0, end-start, 180)
	g.Set("strokeStyle", "#6cd6ad")
	g.Call("beginPath")
	for i, p := range src.Peaks {
		x := (float64(i) + .5) * 900 / float64(len(src.Peaks))
		height := float64(p) * 75
		g.Call("moveTo", x, 90-height)
		g.Call("lineTo", x, 90+height)
	}
	g.Call("stroke")
	g.Set("strokeStyle", "#e6b457")
	g.Set("lineWidth", 3)
	for _, x := range []float64{start, end} {
		g.Call("beginPath")
		g.Call("moveTo", x, 0)
		g.Call("lineTo", x, 180)
		g.Call("stroke")
	}
	g.Set("lineWidth", 1)
	g.Set("fillStyle", "#e9e6d6")
	g.Set("font", "13px monospace")
	g.Call("fillText", fmt.Sprintf("%s · %.2f s · %d Hz", src.Name, float64(src.Asset.Frames)/float64(src.Asset.RateHz), src.Asset.RateHz), 12, 22)
}
func (u *samplerUI) clockLoop() {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			if u.closed.Load() {
				return
			}
			if u.recording && u.now() >= u.origin+float64(u.perf.Frames)/padsampler.Rate {
				u.stop()
			}
			now := u.now()
			active := u.voices[:0]
			for _, v := range u.voices {
				if v.end <= now {
					v.source.Call("disconnect")
					v.gain.Call("disconnect")
				} else {
					active = append(active, v)
				}
			}
			u.voices = active
			for pad := 1; pad <= 16; pad++ {
				playing := false
				for _, v := range active {
					playing = playing || (v.pad == pad && v.start <= now)
				}
				button := u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad))
				button.Call("setAttribute", "data-playing", strconv.FormatBool(playing))
			}
			u.draw()
		}
	}
}
