//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	padsampler "m31labs.dev/cicada/host/padsampler/protocol"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/scene"
)

type samplerProjection struct {
	Revision     string                                `json:"revision"`
	BankRevision string                                `json:"bankRevision"`
	Bank         padsampler.Bank[padsampler.AudioInfo] `json:"bank"`
	Sounds       []string                              `json:"sounds"`
	Tempo        int                                   `json:"tempo"`
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
	started            bool
}
type samplerUI struct {
	ctx                             context.Context
	cancel                          context.CancelFunc
	closed                          atomic.Bool
	waveEligible                    atomic.Bool
	inputEpoch                      atomic.Uint64
	inputSeen                       uint64
	waveCapture                     atomic.Int64
	executor                        *samplerExecutor
	effectValid                     func() bool
	loadMu                          sync.Mutex
	loadSnapshotMu                  sync.Mutex
	loadSnapshot                    samplerLoadSnapshot
	loader                          *samplerLoader[samplerLoadResult]
	loadMailbox                     samplerMailbox[samplerLoadDelivery]
	resampleMailbox                 samplerMailbox[string]
	resetRecordRevision             bool
	root, panel, audio, wave        js.Value
	bindings                        []samplerBinding
	state                           samplerProjection
	buffers                         map[string]js.Value
	voices                          []*samplerVoice
	perf                            padsampler.Performance
	keyPads                         map[int]bool
	inputKeys                       map[int]uint64
	keySeq                          uint64
	inputPointers                   map[int]uint64
	inputPointerPads                map[int]int
	pointerSeq                      uint64
	savedPerf                       string
	initialized, recording, posting bool
	postInFlight                    bool
	origin                          float64
	recordRevision                  string
	recordSourceRevision            string
	recordTempo                     int
	waveKey                         string
	drag                            string
	dragBefore                      int64
	dragOffset                      float64
	dragPointer                     int
	waveInputPointer                int
	waveInputActive                 bool
	playingPads                     [16]bool
	sceneWake                       chan struct{}
	sceneMu                         sync.Mutex
	sceneDown                       [16]bool
	sceneGeneration                 [16]uint64
	heldPads                        map[int]bool
	padOwners                       map[int]map[string]bool
	frame                           js.Value
	frameNonce                      string
	frameReady                      bool
	framePads                       samplerFrameHolds
	frameFocusStart                 js.Value
	frameSeq                        int
	frameLoads                      int
	frameLoadFn                     js.Func
	frameLast                       time.Time
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
	u.wave = root.Call("querySelector", "[data-sampler-wave]")

	u.sceneWake = make(chan struct{}, 1)
	u.frame = js.Undefined()
	u.heldPads = make(map[int]bool)
	u.padOwners = make(map[int]map[string]bool)
	u.inputPointers = make(map[int]uint64)
	u.inputPointerPads = make(map[int]int)
	u.initSamplerLoader()
	u.executor = newSamplerExecutor(256, func(c samplerCommand, valid func() bool) {
		u.effectValid = valid
		defer func() {
			u.effectValid = nil
			u.executor.publishDirty(u.unsavedPerformance())
		}()
		u.runSamplerCommand(c)
	}, u.samplerCleanup)
	u.executor.ready = func() {
		if !u.closed.Load() {
			safeSamplerEffect(u.draw)
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-u.sceneWake:
				u.dispatchSampler(samplerCommand{kind: "scene-publish", coalesce: true})
			}
		}
	}()
	u.listen(js.Global(), "message", u.sceneMessage)
	u.listen(js.Global().Get("document"), "visibilitychange", func(js.Value) {
		hidden := js.Global().Get("document").Get("hidden").Bool()
		u.dispatchSampler(samplerCommand{kind: "visibility", flag: hidden})
	})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				u.sceneMu.Lock()
				frame, nonce, last := u.frame, u.frameNonce, u.frameLast
				u.sceneMu.Unlock()
				if workspacePresent(frame) && !js.Global().Get("document").Get("hidden").Bool() && time.Since(last) > 10*time.Second {
					u.dispatchSampler(samplerCommand{kind: "scene-timeout", text: nonce})
				}
			}
		}
	}()

	u.listen(root, "pointerdown", func(e js.Value) {
		target := e.Get("target").Call("closest", "[data-sampler-pad]")
		if target.IsNull() {
			return
		}
		e.Call("preventDefault")
		pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
		pointer := e.Get("pointerId").Int()
		u.pointerSeq++
		epoch := u.pointerSeq
		u.inputPointers[pointer] = epoch
		u.inputPointerPads[pointer] = pad
		u.audio.Call("resume")
		if u.inputPointers[pointer] != epoch {
			return
		}
		target.Call("setPointerCapture", e.Get("pointerId"))
		if u.inputPointers[pointer] == epoch {
			u.dispatchSampler(samplerCommand{kind: "pad-down", pad: pad, number: pointer, epoch: epoch})
		}
	})
	u.listen(root, "pointerup", func(e js.Value) {
		target := e.Get("target").Call("closest", "[data-sampler-pad]")
		if target.IsNull() {
			return
		}
		pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
		pointer := e.Get("pointerId").Int()
		if u.inputPointerPads[pointer] != pad {
			return
		}
		epoch := u.inputPointers[pointer]
		delete(u.inputPointers, pointer)
		delete(u.inputPointerPads, pointer)
		if epoch != 0 {
			u.dispatchSampler(samplerCommand{kind: "pad-up", pad: pad, number: pointer, epoch: epoch})
		}
		if target.Call("hasPointerCapture", e.Get("pointerId")).Bool() {
			target.Call("releasePointerCapture", e.Get("pointerId"))
		}
	})
	for _, event := range []string{"pointercancel", "lostpointercapture"} {
		event := event
		u.listen(root, event, func(e js.Value) {
			target := e.Get("target").Call("closest", "[data-sampler-pad]")
			if !target.IsNull() {
				pad, _ := strconv.Atoi(target.Call("getAttribute", "data-sampler-pad").String())
				pointer := e.Get("pointerId").Int()
				if u.inputPointerPads[pointer] != pad {
					return
				}
				if event == "lostpointercapture" && target.Call("hasPointerCapture", pointer).Bool() {
					return
				}
				epoch := u.inputPointers[pointer]
				delete(u.inputPointers, pointer)
				delete(u.inputPointerPads, pointer)
				if epoch != 0 {
					u.dispatchSampler(samplerCommand{kind: "pad-up", pad: pad, number: pointer, epoch: epoch})
				}
			}
		})
	}
	u.listen(root, "click", func(e js.Value) {
		target := e.Get("target")
		if toggle := target.Call("closest", "[data-sampler-scene-toggle]"); !toggle.IsNull() {
			u.dispatchSampler(samplerCommand{kind: "scene-toggle"})
			return
		}

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
			u.dispatchSampler(samplerCommand{kind: control.Call("getAttribute", "data-sampler-control").String()})
			return
		}
		if pad := target.Call("closest", "[data-sampler-pad]"); !pad.IsNull() && e.Get("detail").Int() == 0 {
			u.audio.Call("resume")
			index, _ := strconv.Atoi(pad.Call("getAttribute", "data-sampler-pad").String())
			// An accessibility click has no matching key-up; play the bounded sample to its natural end.
			u.dispatchSampler(samplerCommand{kind: "pad-accessible", pad: index})
		}
	})
	u.listen(root, "submit", func(e js.Value) {
		if e.Get("target").Call("closest", "[data-sampler-resample]").IsNull() {
			return
		}
		e.Call("preventDefault")
		e.Call("stopImmediatePropagation")
		form := e.Get("target")
		values := map[string]string{"place": "", "source_revision": workspaceFieldValue(form, "source_revision"), "__cicada_location": workspaceLocation()}
		for _, key := range []string{"csrf_token", "newName", "track", "at", "destination"} {
			values[key] = form.Call("querySelector", "[name="+key+"]").Get("value").String()
		}
		if form.Call("querySelector", "[name=place]").Get("checked").Bool() {
			values["place"] = "on"
		}
		data, _ := json.Marshal(values)
		u.dispatchSampler(samplerCommand{kind: "resample", text: string(data)})
	})
	document := js.Global().Get("document")
	keys := "1234qwerasdfzxcv"
	u.inputKeys = make(map[int]uint64)
	for _, event := range []string{"keydown", "keyup"} {
		event := event
		u.listen(document, event, func(e js.Value) {
			if !root.Get("isConnected").Bool() {
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
			if event == "keydown" {
				target := e.Get("target")
				if !target.Call("closest", "input,textarea,select,[contenteditable=true]").IsNull() || e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() || e.Get("altKey").Bool() {
					return
				}
				if key == "escape" && event == "keydown" {
					u.dispatchSampler(samplerCommand{kind: "stop"})
					return
				}
			}
			if len(key) != 1 {
				return
			}
			i := strings.Index(keys, key)
			if i < 0 {
				return
			}
			if event == "keyup" {
				if u.inputKeys[i+1] != 0 {
					delete(u.inputKeys, i+1)
					u.dispatchSampler(samplerCommand{kind: "key-up", pad: i + 1})
					e.Call("preventDefault")
				}
				return
			}
			// Letter keys belong to Live unless the Sampler region has focus.
			if !ownsKeyboard(keysSampler) {
				return
			}
			e.Call("preventDefault")
			e.Call("stopImmediatePropagation")
			if !e.Get("repeat").Bool() {
				u.keySeq++
				epoch := u.keySeq
				u.inputKeys[i+1] = epoch
				u.audio.Call("resume")
				if u.inputKeys[i+1] != epoch {
					return
				}
				velocity := 1.
				if e.Get("shiftKey").Bool() {
					velocity = .65
				}
				u.dispatchSampler(samplerCommand{kind: "key-down", pad: i + 1, value: velocity})
			}
		})
	}
	u.listen(js.Global(), "blur", func(js.Value) {
		if samplerFrameOwnsFocus() {
			u.samplerFrameFocusTransfer()
		} else {
			u.dispatchSampler(samplerCommand{kind: "blur"})
		}
	})
	watchKeyboardOwner(u.listen, keysSampler, func() {
		if len(u.inputKeys) == 0 {
			return
		}
		clear(u.inputKeys)
		u.dispatchSampler(samplerCommand{kind: "key-clear"})
	})

	u.listen(u.wave, "pointerdown", func(e js.Value) {
		if !u.samplerWaveMayCapture(e.Get("button").Int(), e.Get("isPrimary").Bool()) {
			return
		}
		box := u.wave.Call("querySelector", "svg").Call("getBoundingClientRect")
		if box.Get("width").Float() <= 0 {
			return
		}
		boundary := ""
		if h := e.Get("target").Call("closest", "[data-sampler-boundary]"); !h.IsNull() {
			boundary = h.Call("getAttribute", "data-sampler-boundary").String()
		}
		u.waveInputPointer, u.waveInputActive = e.Get("pointerId").Int(), true
		u.waveCapture.Store(int64(u.waveInputPointer) + 1)
		u.wave.Call("setPointerCapture", u.waveInputPointer)
		e.Call("preventDefault")
		u.dispatchSampler(samplerCommand{kind: "wave-down", number: u.waveInputPointer, value: e.Get("clientX").Float(), text: boundary})
	})
	u.listen(u.wave, "pointermove", func(e js.Value) {
		if u.waveInputActive && e.Get("pointerId").Int() == u.waveInputPointer {
			u.dispatchSampler(samplerCommand{kind: "wave-move", number: u.waveInputPointer, value: e.Get("clientX").Float()})
		}
	})
	for _, event := range []string{"pointerup", "pointercancel", "lostpointercapture"} {
		event := event
		u.listen(u.wave, event, func(e js.Value) {
			if u.waveInputActive && e.Get("pointerId").Int() == u.waveInputPointer {
				if event == "lostpointercapture" && u.wave.Call("hasPointerCapture", u.waveInputPointer).Bool() {
					return
				}
				u.waveInputActive = false
				u.waveCapture.Store(0)
				u.dispatchSampler(samplerCommand{kind: "wave-end", number: u.waveInputPointer, flag: event != "pointerup"})
			}
		})
	}
	u.listen(u.wave, "keydown", func(e js.Value) {
		key := e.Get("key").String()
		if u.waveInputActive {
			if key != "Escape" && !strings.HasPrefix(key, "Arrow") && key != "Home" && key != "End" {
				return
			}
		} else {
			if !u.waveEligible.Load() || e.Get("metaKey").Bool() || e.Get("ctrlKey").Bool() {
				return
			}
			if key != "ArrowLeft" && key != "ArrowRight" && key != "ArrowUp" && key != "ArrowDown" && key != "Home" && key != "End" {
				return
			}
		}
		h := e.Get("target").Call("closest", "[data-sampler-boundary]")
		name := ""
		if !h.IsNull() {
			name = h.Call("getAttribute", "data-sampler-boundary").String()
		}
		if !u.waveInputActive && name == "" {
			return
		}
		mods := 0
		if e.Get("shiftKey").Bool() {
			mods |= 1
		}
		if e.Get("altKey").Bool() {
			mods |= 2
		}
		e.Call("preventDefault")
		e.Call("stopImmediatePropagation")
		u.dispatchSampler(samplerCommand{kind: "wave-key", text: key, owner: name, number: mods})
	})
	for _, event := range []string{"input", "change"} {
		u.listen(u.panel, event, func(e js.Value) {
			if !e.Get("target").Call("closest", "#sampler-pad-editor").IsNull() {
				u.dispatchSampler(samplerCommand{kind: "draw"})
			}
		})
	}
	go u.refreshLoop()
	go u.clockLoop()
	releaseUnsaved := holdUnsavedWork("sampler", u.executor.dirtySnapshot)
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		releaseUnsaved()
		if u.executor != nil {
			cancel()
			u.executor.close()
			for _, b := range u.bindings {
				b.node.Call("removeEventListener", b.event, b.fn)
				b.node.Call("removeEventListener", b.event, b.fn, true)
				b.fn.Release()
			}
			return
		}
		u.closeSceneFrame()
		u.stop()
		u.disposeSamplerVoices()
		cancel()
		for _, b := range u.bindings {
			b.node.Call("removeEventListener", b.event, b.fn)
			b.node.Call("removeEventListener", b.event, b.fn, true)
			b.fn.Release()
		}
		u.audio.Call("close")
	}), nil
}

func safeSamplerEffect(effect func()) {
	defer func() { _ = recover() }()
	effect()
}

// samplerCleanup is the executor's overflow and terminal barrier. Cleanup
// remains in the revoked generation until every owned voice has been stopped.
func (u *samplerUI) samplerCleanup() bool {
	u.inputEpoch.Add(1)
	u.waveEligible.Store(false)
	if pointer := u.waveCapture.Swap(0); pointer != 0 {
		safeSamplerEffect(func() {
			id := int(pointer - 1)
			if u.wave.Call("hasPointerCapture", id).Bool() {
				u.wave.Call("releasePointerCapture", id)
			}
		})
	}
	safeSamplerEffect(func() { u.finishBoundary(true) })
	safeSamplerEffect(u.stop)
	safeSamplerEffect(u.closeSceneFrame)
	u.disposeSamplerVoices()
	u.keyPads = make(map[int]bool)
	u.loadMailbox.clear()
	if u.closed.Load() {
		u.resampleMailbox.clear()
		safeSamplerEffect(func() { u.audio.Call("close") })
	} else {
		if epoch, pending := u.resampleMailbox.renotify(); pending {
			u.posting = true
			go u.notifySamplerResample(epoch)
		} else if !u.postInFlight {
			u.posting = false
		}
		if u.loader != nil {
			safeSamplerEffect(func() { u.requestSamplerLoad(true) })
		}
	}
	return u.unsavedPerformance()
}

// Child keyup events cannot identify which parent key press they belong to.
func (u *samplerUI) samplerFrameFocusTransfer() {
	if len(u.inputKeys) != 0 {
		clear(u.inputKeys)
		u.dispatchSampler(samplerCommand{kind: "key-clear"})
	}
}

func (u *samplerUI) samplerWaveMayCapture(button int, primary bool) bool {
	return u.waveEligible.Load() && !u.waveInputActive && button == 0 && primary
}

// dispatchSampler centralizes extracted state actions. The executor is enabled
// only after every writer has been migrated to this path.
func (u *samplerUI) dispatchSampler(c samplerCommand) {
	u.tryDispatchSampler(c)
}

func (u *samplerUI) tryDispatchSampler(c samplerCommand) bool {
	if u.executor != nil {
		// Any state action can finish or record a performance indirectly; keep
		// synchronous refresh checks conservative until it completes.
		c.dirties = true
		if !u.executor.submit(c) {
			u.inputEpoch.Add(1)
			u.waveEligible.Store(false)
			return false
		}
		return true
	}
	u.runSamplerCommand(c)
	return true
}

func (u *samplerUI) runSamplerCommand(c samplerCommand) {
	switch {
	case strings.HasPrefix(c.kind, "scene:"):
		u.handleSamplerSceneCommand(c)
	case c.kind == "pad-down" || c.kind == "pad-up":
		u.handleSamplerPointer(c)
	case c.kind == "pad-accessible":
		if u.hit(c.pad, 1) && !u.heldPads[c.pad] {
			u.scenePulse(c.pad)
		}
	case c.kind == "scene-toggle":
		toggle := u.root.Call("querySelector", "[data-sampler-scene-toggle]")
		if workspacePresent(u.frame) {
			u.closeSceneFrame()
		} else if u.openSceneFrame() {
			toggle.Set("textContent", "Hide 3D surface")
			toggle.Call("setAttribute", "aria-pressed", "true")
		}
	case c.kind == "scene-load":
		u.samplerFrameLoaded(c.text)
	case c.kind == "scene-publish":
		u.publishSceneState()
	case c.kind == "scene-pulse-end":
		u.samplerScenePulseEnd(c.pad, c.epoch)
	case c.kind == "scene-timeout":
		u.sceneMu.Lock()
		frame, nonce, last := u.frame, u.frameNonce, u.frameLast
		u.sceneMu.Unlock()
		if nonce == c.text && !js.Global().Get("document").Get("hidden").Bool() && time.Since(last) > 10*time.Second && u.closeSceneFrameExpected(frame, nonce) {
			u.status("3D surface unavailable. The numbered pads remain playable.")
		}
	case c.kind == "resample":
		u.resample(c.text)
	case c.kind == "resample-result":
		if payload, ok := u.resampleMailbox.peek(); ok {
			u.finishSamplerResample(payload)
			if u.samplerEffectsAllowed() {
				u.resampleMailbox.take()
				u.postInFlight = false
			}
		}
	case c.kind == "refresh-request":
		u.requestSamplerLoad(c.flag)
	case c.kind == "refresh-result":
		u.finishSamplerLoad()
	case c.kind == "draw":
		u.draw()
	case c.kind == "key-down" || c.kind == "key-up" || c.kind == "key-clear":
		u.handleSamplerKey(c)
	case c.kind == "record" || c.kind == "stop" || c.kind == "play":
		u.handleSamplerTransport(c)
	case c.kind == "visibility" || c.kind == "blur":
		u.handleSamplerLifecycle(c)
	case strings.HasPrefix(c.kind, "wave-"):
		u.handleSamplerWave(c)
	case c.kind == "clock":
		u.samplerClockTick()
	}
}

// Pointer callbacks perform browser gesture and capture effects synchronously,
// then pass only copied primitives to the sampler state path.
func (u *samplerUI) handleSamplerPointer(c samplerCommand) {
	owner := fmt.Sprintf("pointer:%d:%d", c.number, c.epoch)
	switch c.kind {
	case "pad-down":
		u.pressPad(c.pad, owner, 1)
	case "pad-up":
		u.releasePadOwner(c.pad, owner)
	}
}

func (u *samplerUI) handleSamplerTransport(c samplerCommand) {
	switch c.kind {
	case "record":
		u.record()
	case "stop":
		u.stop()
	case "play":
		u.play()
	}
}

func (u *samplerUI) handleSamplerLifecycle(c samplerCommand) {
	if c.kind == "blur" || (c.kind == "visibility" && c.flag) {
		u.finishBoundary(true)
		u.stop()
	}
	if c.kind != "visibility" {
		return
	}
	u.sceneMu.Lock()
	if c.flag {
		u.framePads = make(samplerFrameHolds)
	} else {
		u.frameLast = time.Now()
	}
	u.sceneMu.Unlock()
}

func (u *samplerUI) handleSamplerKey(c samplerCommand) {
	switch c.kind {
	case "key-down":
		if u.keyPads == nil {
			u.keyPads = make(map[int]bool)
		}
		u.keyPads[c.pad] = true
		u.pressPad(c.pad, fmt.Sprintf("key:%d", c.pad), c.value)
	case "key-up":
		delete(u.keyPads, c.pad)
		u.releasePadOwner(c.pad, fmt.Sprintf("key:%d", c.pad))
	case "key-clear":
		for pad := range u.keyPads {
			delete(u.keyPads, pad)
			u.releasePadOwner(pad, fmt.Sprintf("key:%d", pad))
		}
	}
}

func (u *samplerUI) listen(node js.Value, event string, handler func(js.Value)) {
	fn := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.closed.Load() && len(args) > 0 {
			u.syncSamplerInput()
			handler(args[0])
		}
		return nil
	})
	node.Call("addEventListener", event, fn)
	u.bindings = append(u.bindings, samplerBinding{node, event, fn})
}

// Event-side tokens are never read by the worker. Rejection revokes their
// epoch immediately; the next synchronous browser callback clears them.
func (u *samplerUI) syncSamplerInput() {
	epoch := u.inputEpoch.Load()
	if epoch != u.inputSeen {
		clear(u.inputKeys)
		clear(u.inputPointers)
		clear(u.inputPointerPads)
		u.waveInputActive = false
		u.inputSeen = epoch
	}
}
func (u *samplerUI) status(text string) {
	if !u.closed.Load() {
		u.root.Call("querySelector", "[data-sampler-status]").Set("textContent", text)
	}
}
func (u *samplerUI) refreshLoop() {
	for {
		if u.loader != nil {
			u.dispatchSampler(samplerCommand{kind: "refresh-request", coalesce: true})
		} else if err := u.load(); err != nil && u.ctx.Err() == nil {
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
	var state samplerProjection
	if err := readNativeJSON(u.ctx, "/api/sampler", 15*time.Second, 2<<20, &state); err != nil {
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
		u.savedPerf = u.perfFingerprint()
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
	u.draw()
	u.root.Call("setAttribute", "data-sampler-bank-revision", state.BankRevision)
	u.root.Call("setAttribute", "data-sampler-ready", "true")
	if changed && !u.recording {
		u.status("Pads ready · browser audio · record a performance, then resample it into Session.")
	}
	return nil
}
func (u *samplerUI) now() float64 { return u.audio.Get("currentTime").Float() }

// Each input source owns its hold until it releases; one source cannot stop another.
func (u *samplerUI) pressPad(pad int, owner string, velocity float64) {
	if pad < 1 || pad > 16 {
		return
	}
	if u.padOwners[pad] == nil {
		u.padOwners[pad] = make(map[string]bool)
	}
	if u.padOwners[pad][owner] {
		return
	}
	if !u.hit(pad, velocity) {
		return
	}
	u.padOwners[pad][owner] = true
	u.heldPads[pad] = true
	u.scenePress(pad, true)
}
func (u *samplerUI) releasePadOwner(pad int, owner string) {
	owners := u.padOwners[pad]
	if !owners[owner] {
		return
	}
	delete(owners, owner)
	if len(owners) == 0 {
		delete(u.padOwners, pad)
		u.releasePad(pad)
	}
}
func (u *samplerUI) hit(pad int, velocity float64) bool {
	if pad < 1 || pad > 16 || len(u.state.Sounds) != 16 {
		return false
	}
	id := u.state.Sounds[pad-1]
	buffer, ok := u.buffers[id]
	if !ok {
		u.status("This pad is still preparing. Try again when the bank is ready.")
		return false
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
			return false
		}
		when = u.origin + float64(frame)/padsampler.Rate
		index = len(u.perf.Hits)
		u.perf.Hits = append(u.perf.Hits, padsampler.Hit{Sound: id, Pad: pad, Frame: frame, Velocity: velocity, Choke: p.Choke})
		u.events()
	}
	if u.startVoice(buffer, pad, p.Choke, p.Mode, velocity, when, index) == nil {
		if index >= 0 {
			u.perf.Hits = u.perf.Hits[:index]
			u.events()
		}
		u.status("Could not start pad audio.")
		return false
	}
	return true
}
func (u *samplerUI) startVoice(buffer js.Value, pad, choke int, mode string, velocity, when float64, hit int) (voice *samplerVoice) {
	for _, v := range u.voices {
		if v.end > when && (v.pad == pad || (choke > 0 && v.choke == choke)) {
			u.release(v, when)
		}
	}
	v := &samplerVoice{source: js.Undefined(), gain: js.Undefined(), pad: pad, choke: choke, mode: mode, volume: velocity, start: when, end: when + buffer.Get("duration").Float(), hit: hit}
	u.voices = append(u.voices, v)
	complete := false
	defer func() {
		if recover() != nil || !complete {
			u.disposeSamplerVoice(v)
			u.playingPads[pad-1] = false
			for i, candidate := range u.voices {
				if candidate == v {
					u.voices = append(u.voices[:i], u.voices[i+1:]...)
					break
				}
			}
			voice = nil
		}
	}()
	source := u.audio.Call("createBufferSource")
	v.source = source
	if !u.samplerEffectsAllowed() {
		return nil
	}
	source.Set("buffer", buffer)
	if !u.samplerEffectsAllowed() {
		return nil
	}
	gain := u.audio.Call("createGain")
	v.gain = gain
	if !u.samplerEffectsAllowed() {
		return nil
	}
	gain.Get("gain").Call("setValueAtTime", velocity, when)
	if !u.samplerEffectsAllowed() {
		return nil
	}
	source.Call("connect", gain)
	if !u.samplerEffectsAllowed() {
		return nil
	}
	gain.Call("connect", u.audio.Get("destination"))
	if !u.samplerEffectsAllowed() {
		return nil
	}
	v.started = true
	source.Call("start", when)
	if !u.samplerEffectsAllowed() {
		return nil
	}
	u.playingPads[pad-1] = true
	u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad)).Call("setAttribute", "data-playing", "true")
	if !u.samplerEffectsAllowed() {
		return nil
	}
	complete = true
	return v
}

func (u *samplerUI) samplerEffectsAllowed() bool {
	return !u.closed.Load() && (u.effectValid == nil || u.effectValid())
}
func (u *samplerUI) release(v *samplerVoice, when float64) {
	if when >= v.end {
		return
	}
	if !workspacePresent(v.source) || !v.started {
		v.end = when
		return
	}
	if !workspacePresent(v.gain) {
		samplerSafeNodeCall(v.source, "stop", when)
		v.end = when
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

// samplerSafeNodeCall also handles a source that failed before start completed.
func samplerSafeNodeCall(node js.Value, method string, args ...any) {
	if !workspacePresent(node) {
		return
	}
	defer func() { _ = recover() }()
	node.Call(method, args...)
}

func (u *samplerUI) disposeSamplerVoice(v *samplerVoice) {
	// A reentrant teardown can run inside start before that call returns. Stop
	// again on the post-call cleanup even if an earlier cleanup cleared started.
	samplerSafeNodeCall(v.source, "stop")
	samplerSafeNodeCall(v.source, "disconnect")
	samplerSafeNodeCall(v.gain, "disconnect")
	v.started = false
	v.end = 0
}

// Terminal and overflow cleanup may run after any partially completed Web Audio
// operation. Nodes are disposed idempotently, including not-yet-started voices.
func (u *samplerUI) disposeSamplerVoices() {
	for _, v := range u.voices {
		u.disposeSamplerVoice(v)
	}
	u.voices = nil
}
func (u *samplerUI) releasePad(pad int) {
	delete(u.heldPads, pad)
	u.scenePress(pad, false)
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
	u.recordSourceRevision = workspaceFieldValue(u.root.Call("querySelector", "form"), "source_revision")
	u.recordTempo = u.state.Tempo
	u.recording = true
	u.events()
	u.status(fmt.Sprintf("Recording %d bars · pads and Shift for quieter hits · Stop pads finishes early.", bars))
}
func (u *samplerUI) stop() {
	u.padOwners = make(map[int]map[string]bool)
	for pad := range u.heldPads {
		u.releasePad(pad)
	}
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
		if v == nil {
			u.stop()
			u.status("Could not start performance audio.")
			return
		}
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

type samplerResampleCompletion struct {
	Values    map[string]string     `json:"values"`
	Submitted string                `json:"submitted"`
	Result    samplerResampleResult `json:"result"`
	Failure   string                `json:"failure"`
}

func (u *samplerUI) resample(snapshot string) {
	if u.posting {
		return
	}
	var values map[string]string
	if json.Unmarshal([]byte(snapshot), &values) != nil || values == nil {
		return
	}
	u.stop()
	if !u.samplerEffectsAllowed() {
		return
	}
	if len(u.perf.Hits) == 0 {
		u.status("Record a pad performance before resampling.")
		return
	}
	u.posting = true
	u.waveEligible.Store(false)
	data, _ := json.Marshal(u.perf)
	sourceRevision := u.recordSourceRevision
	if sourceRevision == "" {
		sourceRevision = values["source_revision"]
	}
	revision := u.recordRevision
	if values["place"] != "on" {
		revision = u.state.Revision
	}
	values["source_revision"], values["revision"] = sourceRevision, revision
	values["action"], values["performance"], values["bankRevision"] = "resample", string(data), u.state.BankRevision
	u.status("Resampling the recorded pad sounds…")
	if !u.samplerEffectsAllowed() {
		return
	}
	u.postInFlight = true
	go func() {
		result, err := postSamplerResample(u.ctx, values)
		completion := samplerResampleCompletion{Values: values, Submitted: string(data), Result: result}
		if err != nil {
			consoleWarn("Sampler resample", err)
			completion.Failure = resampleFailureText(err) + " The outcome may be unknown; refresh before retrying."
		}
		encoded, _ := json.Marshal(completion)
		if !u.closed.Load() {
			if epoch, notify := u.resampleMailbox.put(string(encoded)); notify {
				go u.notifySamplerResample(epoch)
			}
		}
	}()
}

func (u *samplerUI) finishSamplerResample(payload string) {
	var completion samplerResampleCompletion
	if json.Unmarshal([]byte(payload), &completion) != nil {
		return
	}
	u.posting = false
	defer u.draw()
	if completion.Failure != "" {
		u.status(completion.Failure)
		return
	}
	if !completion.Result.OK {
		u.status("Resampling failed; your performance is retained. " + completion.Result.Message)
		return
	}
	u.status("Resampled · open Session to arrange the new clip.")
	if !u.samplerEffectsAllowed() {
		return
	}
	u.savedPerf = completion.Submitted
	if form := u.root.Call("querySelector", "[data-sampler-resample]"); !form.IsNull() {
		acknowledgeSubmitted(form, completion.Values)
	}
	if !u.samplerEffectsAllowed() {
		return
	}
	u.recordRevision = ""
	u.recordSourceRevision = ""
	if u.loader != nil {
		u.resetRecordRevision = true
		u.requestSamplerLoad(true)
	} else {
		_ = u.load()
		u.recordRevision = u.state.Revision
	}
	workspaceRefreshRequested.Update(func(n uint64) uint64 { return n + 1 })
}

func (u *samplerUI) notifySamplerResample(epoch uint64) {
	for !u.closed.Load() && u.resampleMailbox.current(epoch) {
		if u.tryDispatchSampler(samplerCommand{kind: "resample-result"}) {
			return
		}
		timer := time.NewTimer(30 * time.Millisecond)
		select {
		case <-u.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// perfFingerprint identifies the performance's content, so every recorded or
// edited hit changes it and two different performances of equal length differ.
func (u *samplerUI) perfFingerprint() string {
	data, _ := json.Marshal(u.perf)
	return string(data)
}

// unsavedPerformance is the Sampler's entry in the unsaved-work registry: hits
// that exist and differ from the performance the service last accepted.
func (u *samplerUI) unsavedPerformance() bool {
	return len(u.perf.Hits) > 0 && u.perfFingerprint() != u.savedPerf
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
func (u *samplerUI) selectedSource() (padsampler.Source[padsampler.AudioInfo], bool) {
	form := u.editor()
	if form.IsNull() {
		return padsampler.Source[padsampler.AudioInfo]{}, false
	}
	return u.state.Bank.Source(form.Call("querySelector", "[name=source]").Get("value").String())
}
func (u *samplerUI) setBoundary(name string, frame int64) {
	src, ok := u.selectedSource()
	if !ok || src.Asset.Frames < 1 {
		return
	}
	if name == "start" {
		frame = max(0, min(frame, u.bound("end")-1))
	} else {
		frame = max(u.bound("start")+1, min(frame, src.Asset.Frames))
	}
	input := u.editor().Call("querySelector", "[name="+name+"]")
	value := strconv.FormatInt(frame, 10)
	if input.Get("value").String() == value {
		return
	}
	input.Set("value", value)
	input.Call("dispatchEvent", js.Global().Get("Event").New("input", map[string]any{"bubbles": true}))
	u.draw()
}
func (u *samplerUI) moveBoundary(clientX float64) {
	src, ok := u.selectedSource()
	if !ok {
		return
	}
	box := u.wave.Call("querySelector", "svg").Call("getBoundingClientRect")
	if box.Get("width").Float() <= 0 {
		return
	}
	frame := int64(math.Round((clientX-box.Get("left").Float())/box.Get("width").Float()*float64(src.Asset.Frames) - u.dragOffset))
	u.setBoundary(u.drag, frame)
}
func (u *samplerUI) handleSamplerWave(c samplerCommand) {
	switch c.kind {
	case "wave-key":
		u.boundaryKey(c)
	case "wave-down":
		if u.posting || u.drag != "" {
			return
		}
		src, ok := u.selectedSource()
		if !ok {
			return
		}
		box := u.wave.Call("querySelector", "svg").Call("getBoundingClientRect")
		if box.Get("width").Float() <= 0 {
			return
		}
		frame := (c.value - box.Get("left").Float()) / box.Get("width").Float() * float64(src.Asset.Frames)
		u.drag = "start"
		if math.Abs(frame-float64(u.bound("end"))) < math.Abs(frame-float64(u.bound("start"))) {
			u.drag = "end"
		}
		u.dragOffset = 0
		if c.text == "start" || c.text == "end" {
			u.drag = c.text
			u.dragOffset = frame - float64(u.bound(u.drag))
		}
		u.dragBefore = u.bound(u.drag)
		u.dragPointer = c.number
		u.wave.Call("querySelector", "[data-sampler-boundary="+u.drag+"]").Call("focus", map[string]any{"preventScroll": true})
		u.moveBoundary(c.value)
	case "wave-move":
		if u.drag != "" && c.number == u.dragPointer {
			u.moveBoundary(c.value)
		}
	case "wave-end":
		if u.drag != "" && c.number == u.dragPointer {
			u.finishBoundary(c.flag)
		}
	}
}
func (u *samplerUI) finishBoundary(cancel bool) {
	name := u.drag
	if name == "" {
		return
	}
	u.drag = ""
	if cancel {
		u.setBoundary(name, u.dragBefore)
	}
	if u.wave.Call("hasPointerCapture", u.dragPointer).Bool() {
		u.wave.Call("releasePointerCapture", u.dragPointer)
	}
}
func (u *samplerUI) boundaryKey(c samplerCommand) {
	key := c.text
	if u.drag != "" {
		if key == "Escape" {
			u.finishBoundary(true)
		}
		return
	}
	if c.owner == "" || u.posting {
		return
	}
	src, ok := u.selectedSource()
	if !ok {
		return
	}
	step := int64(max(1, src.Asset.RateHz/1000))
	if c.number&1 != 0 {
		step *= 10
	}
	if c.number&2 != 0 {
		step = 1
	}
	frame := u.bound(c.owner)
	switch key {
	case "ArrowLeft", "ArrowDown":
		frame -= step
	case "ArrowRight", "ArrowUp":
		frame += step
	case "Home":
		frame = 0
	case "End":
		frame = src.Asset.Frames
	default:
		return
	}
	u.setBoundary(c.owner, frame)
}

func (u *samplerUI) draw() {
	if !u.samplerEffectsAllowed() {
		u.waveEligible.Store(false)
		return
	}
	src, ok := u.selectedSource()
	u.waveEligible.Store(ok && !u.posting && src.Asset.Frames > 0)
	start, end := u.bound("start"), u.bound("end")
	key := fmt.Sprintf("%s:%d:%d:%t", src.ID, start, end, u.posting)
	if key == u.waveKey {
		return
	}
	u.waveKey = key
	region := u.wave.Call("querySelector", "[data-sampler-range]")
	peaks := u.wave.Call("querySelector", "[data-sampler-peaks]")
	edges := u.wave.Call("querySelector", "[data-sampler-edges]")
	label := u.root.Call("querySelector", "[data-sampler-wave-label]")
	for _, name := range []string{"start", "end"} {
		u.wave.Call("querySelector", "[data-sampler-boundary="+name+"]").Set("disabled", !ok || u.posting || src.Asset.Frames < 1)
	}
	if !ok || src.Asset.Frames < 1 {
		region.Call("setAttribute", "width", "0")
		peaks.Call("setAttribute", "d", "")
		edges.Call("setAttribute", "d", "")
		label.Set("textContent", "Load a sample to edit its chop boundaries.")
		return
	}
	start = max(0, min(start, src.Asset.Frames-1))
	end = max(start+1, min(end, src.Asset.Frames))
	x1, x2 := float64(start)*900/float64(src.Asset.Frames), float64(end)*900/float64(src.Asset.Frames)
	region.Call("setAttribute", "x", fmt.Sprint(x1))
	region.Call("setAttribute", "width", fmt.Sprint(x2-x1))
	var path strings.Builder
	for i, p := range src.Peaks {
		x := (float64(i) + .5) * 900 / float64(len(src.Peaks))
		height := float64(p) * 75
		fmt.Fprintf(&path, "M%.3f %.3fV%.3f", x, 90-height, 90+height)
	}
	peaks.Call("setAttribute", "d", path.String())
	edges.Call("setAttribute", "d", fmt.Sprintf("M%f 0V180M%f 0V180", x1, x2))
	for _, name := range []string{"start", "end"} {
		h := u.wave.Call("querySelector", "[data-sampler-boundary="+name+"]")
		frame, low, high := start, int64(0), end-1
		if name == "end" {
			frame, low, high = end, start+1, src.Asset.Frames
		}
		h.Get("style").Set("left", fmt.Sprintf("clamp(0px, calc(%f%% - 12px), calc(100%% - 24px))", float64(frame)*100/float64(src.Asset.Frames)))
		h.Get("style").Set("right", "auto")
		h.Call("setAttribute", "aria-valuemin", strconv.FormatInt(low, 10))
		h.Call("setAttribute", "aria-valuemax", strconv.FormatInt(high, 10))
		h.Call("setAttribute", "aria-valuenow", strconv.FormatInt(frame, 10))
		h.Call("setAttribute", "aria-valuetext", fmt.Sprintf("%.3f seconds (%d frames)", float64(frame)/float64(max(1, src.Asset.RateHz)), frame))
	}
	label.Set("textContent", fmt.Sprintf("%s · %.3f–%.3f s · %d Hz · Save pad keeps this chop", src.Name, float64(start)/float64(max(1, src.Asset.RateHz)), float64(end)/float64(max(1, src.Asset.RateHz)), src.Asset.RateHz))
}
func (u *samplerUI) paintPlaying(active []*samplerVoice, now float64) {
	for pad := 1; pad <= 16; pad++ {
		playing := false
		for _, v := range active {
			playing = playing || (v.pad == pad && v.start <= now)
		}
		if playing == u.playingPads[pad-1] {
			continue
		}
		u.playingPads[pad-1] = playing
		u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad)).Call("setAttribute", "data-playing", strconv.FormatBool(playing))
	}
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
			u.dispatchSampler(samplerCommand{kind: "clock", coalesce: true})
		}
	}
}

func (u *samplerUI) samplerClockTick() {
	if !u.recording && len(u.voices) == 0 {
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
	u.paintPlaying(active, now)
}

// scenePress is presentation only; audio and recording keep their existing path.
func (u *samplerUI) scenePress(pad int, down bool) {
	if u.sceneWake == nil || pad < 1 || pad > 16 {
		return
	}
	u.sceneMu.Lock()
	u.sceneDown[pad-1] = down
	u.sceneGeneration[pad-1]++
	u.sceneMu.Unlock()
	select {
	case u.sceneWake <- struct{}{}:
	default:
	}
}
func samplerSceneCommand(pad int, down bool) scene.Command {
	x := float64((pad-1)%4)*1.7 - 2.55
	z := float64((pad-1)/4)*1.65 - 2.475
	y := .38
	if down {
		y = .31
	}
	return scene.Command{Kind: scene.CommandSetTransform, ObjectID: fmt.Sprintf("pad-%d", pad), Data: map[string]any{"x": x, "y": y, "z": z, "rotationX": 0, "rotationY": 0, "rotationZ": 0, "scaleX": 1, "scaleY": 1, "scaleZ": 1, "parentMatrix": nil}}
}

func samplerSceneLightCommand(pad int, down bool) scene.Command {
	x := float64((pad-1)%4)*1.7 - 2.55
	z := float64((pad-1)/4)*1.65 - 2.475
	y := -100.0
	if down {
		y = .405
	}
	return scene.Command{Kind: scene.CommandSetTransform, ObjectID: fmt.Sprintf("light-%d", pad), Data: map[string]any{"x": x, "y": y, "z": z, "rotationX": 0, "rotationY": 0, "rotationZ": 0, "scaleX": 1, "scaleY": 1, "scaleZ": 1, "parentMatrix": nil}}
}

// scenePulse gives click-only assistive activation brief visual feedback without a held gate.
func (u *samplerUI) scenePulse(pad int) {
	if u.sceneWake == nil || pad < 1 || pad > 16 {
		return
	}
	u.sceneMu.Lock()
	u.sceneGeneration[pad-1]++
	generation := u.sceneGeneration[pad-1]
	u.sceneDown[pad-1] = true
	u.sceneMu.Unlock()
	select {
	case u.sceneWake <- struct{}{}:
	default:
	}
	go func() {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-u.ctx.Done():
			return
		case <-timer.C:
		}
		u.dispatchSampler(samplerCommand{kind: "scene-pulse-end", pad: pad, epoch: generation})
	}()
}

func (u *samplerUI) samplerScenePulseEnd(pad int, generation uint64) {
	u.sceneMu.Lock()
	if u.sceneGeneration[pad-1] != generation {
		u.sceneMu.Unlock()
		return
	}
	u.sceneDown[pad-1] = false
	u.sceneMu.Unlock()
	select {
	case u.sceneWake <- struct{}{}:
	default:
	}
}

// openSceneFrame keeps Live and the sampler mounted. The fragment capability is
// never sent to the server; source, origin and nonce are all checked on input.
func (u *samplerUI) openSceneFrame() bool {
	crypto := js.Global().Get("crypto")
	if !workspacePresent(crypto) || crypto.Get("randomUUID").Type() != js.TypeFunction {
		u.status("3D surface unavailable; the numbered pads remain playable.")
		return false
	}
	container := u.root.Call("querySelector", "[data-sampler-scene-container]")
	frame := js.Global().Get("document").Call("createElement", "iframe")
	nonce := crypto.Call("randomUUID").String()
	frame.Call("setAttribute", "title", "3D sampler pads")
	frame.Set("src", "/__sampler/scene-frame#"+nonce)
	u.sceneMu.Lock()
	u.frame = frame
	u.frameNonce = nonce
	u.frameReady = false
	u.framePads = make(samplerFrameHolds)
	u.frameSeq = 0
	u.frameLoads = 0
	u.frameLast = time.Now()
	u.sceneMu.Unlock()
	loadFn := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		u.dispatchSampler(samplerCommand{kind: "scene-load", text: nonce})
		return nil
	})
	u.sceneMu.Lock()
	u.frameLoadFn = loadFn
	u.sceneMu.Unlock()
	frame.Call("addEventListener", "load", loadFn)
	container.Set("hidden", false)
	container.Call("appendChild", frame)
	u.root.Call("setAttribute", "data-sampler-3d", "true")
	return true
}

func (u *samplerUI) samplerFrameLoaded(nonce string) {
	u.sceneMu.Lock()
	frame := u.frame
	if !workspacePresent(frame) || u.frameNonce != nonce {
		u.sceneMu.Unlock()
		return
	}
	u.frameLoads++
	loads := u.frameLoads
	u.sceneMu.Unlock()
	if loads > 1 && u.closeSceneFrameExpected(frame, nonce) {
		u.status("3D surface closed after its frame navigated.")
	}
}
func (u *samplerUI) closeSceneFrame() { u.closeSceneFrameExpected(js.Undefined(), "") }
func (u *samplerUI) closeSceneFrameExpected(expected js.Value, nonce string) bool {
	u.sceneMu.Lock()
	if workspacePresent(expected) && (!u.frame.Equal(expected) || u.frameNonce != nonce) {
		u.sceneMu.Unlock()
		return false
	}
	frame := u.frame
	loadFn := u.frameLoadFn
	holds, ownerNonce := u.framePads, u.frameNonce
	u.frame = js.Undefined()
	u.frameNonce = ""
	u.frameReady = false
	u.framePads = make(samplerFrameHolds)
	u.sceneMu.Unlock()
	for pointer, pad := range holds {
		u.releasePadOwner(pad, samplerFrameOwner(ownerNonce, pointer))
	}
	if workspacePresent(frame) {
		frame.Call("removeEventListener", "load", loadFn)
		loadFn.Release()
		frame.Call("remove")
	}
	if container := u.root.Call("querySelector", "[data-sampler-scene-container]"); workspacePresent(container) {
		container.Set("hidden", true)
	}
	u.root.Call("setAttribute", "data-sampler-3d", "false")
	if toggle := u.root.Call("querySelector", "[data-sampler-scene-toggle]"); workspacePresent(toggle) {
		toggle.Set("textContent", "Show 3D surface")
		toggle.Call("setAttribute", "aria-pressed", "false")
	}
	return true
}
func (u *samplerUI) sceneMessage(e js.Value) {
	origin := js.Global().Get("location").Get("origin").String()
	u.sceneMu.Lock()
	frame, nonce := u.frame, u.frameNonce
	u.sceneMu.Unlock()
	if !workspacePresent(frame) || e.Get("origin").String() != origin || !e.Get("source").Equal(frame.Get("contentWindow")) {
		return
	}
	d := e.Get("data")
	if d.Type() != js.TypeObject || d.IsNull() || workspaceString(d, "protocol") != "cicada-sampler-frame-v1" || workspaceString(d, "nonce") != nonce {
		return
	}
	kind := workspaceString(d, "type")
	c := samplerCommand{kind: kind, text: nonce}
	if kind != "ready" && kind != "heartbeat" && kind != "dispose" {
		padValue := d.Get("pad")
		if padValue.Type() != js.TypeNumber {
			return
		}
		f := padValue.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 || f > 16 {
			return
		}
		c.pad = int(f)
		if kind == "keyup" {
			return
		}
		pointer := d.Get("pointerId")
		if pointer.Type() != js.TypeNumber {
			return
		}
		pf := pointer.Float()
		if math.IsNaN(pf) || math.IsInf(pf, 0) || pf != math.Trunc(pf) || pf < 0 || pf > 2147483647 {
			return
		}
		c.number = int(pf)
		if kind == "down" {
			u.audio.Call("resume")
		}
	}
	c.kind = "scene:" + c.kind
	u.dispatchSampler(c)
}

// Frame messages are authenticated synchronously; only primitive payload data
// crosses into the state handler, which checks the current nonce again.
func (u *samplerUI) handleSamplerSceneCommand(c samplerCommand) {
	kind, nonce := strings.TrimPrefix(c.kind, "scene:"), c.text
	u.sceneMu.Lock()
	frame := u.frame
	valid := workspacePresent(frame) && u.frameNonce == nonce
	u.sceneMu.Unlock()
	if !valid {
		return
	}
	if kind == "ready" || kind == "heartbeat" {
		u.sceneMu.Lock()
		if !u.frame.Equal(frame) || u.frameNonce != nonce {
			u.sceneMu.Unlock()
			return
		}
		u.frameLast = time.Now()
		wasReady := u.frameReady
		if kind == "ready" {
			u.frameReady = true
		}
		u.sceneMu.Unlock()
		if kind == "ready" && !wasReady {
			select {
			case u.sceneWake <- struct{}{}:
			default:
			}
		}
		return
	}
	if kind == "dispose" {
		u.closeSceneFrameExpected(frame, nonce)
		return
	}
	pad := c.pad
	enabled := false
	focus := js.Undefined()
	if kind == "down" {
		button := u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad))
		enabled = workspacePresent(button) && !button.Get("disabled").Bool()
		focus = js.Global().Get("document").Get("activeElement")
	}
	u.sceneMu.Lock()
	if !u.frame.Equal(frame) || u.frameNonce != nonce {
		u.sceneMu.Unlock()
		return
	}
	if u.framePads == nil {
		u.framePads = make(samplerFrameHolds)
	}
	if kind == "down" && enabled {
		first := len(u.framePads) == 0
		if u.framePads.press(c.number, pad) {
			if first {
				u.frameFocusStart = focus
			}
			u.sceneMu.Unlock()
			u.pressPad(pad, samplerFrameOwner(nonce, c.number), 1)
			return
		}
	}
	if kind == "up" && u.framePads.release(c.number, pad) {
		startFocus := u.frameFocusStart
		last := len(u.framePads) == 0
		u.sceneMu.Unlock()
		u.releasePadOwner(pad, samplerFrameOwner(nonce, c.number))
		document := js.Global().Get("document")
		currentFocus := document.Get("activeElement")
		if last && (currentFocus.Equal(frame) || currentFocus.Equal(startFocus)) {
			if button := u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", pad)); workspacePresent(button) {
				button.Call("focus", map[string]any{"preventScroll": true})
			}
		}
		return
	}
	u.sceneMu.Unlock()
}

func samplerFrameOwner(nonce string, pointer int) string {
	return fmt.Sprintf("frame:%s:%d", nonce, pointer)
}
func (u *samplerUI) publishSceneState() {
	u.sceneMu.Lock()
	if !workspacePresent(u.frame) || !u.frameReady {
		u.sceneMu.Unlock()
		return
	}
	frame, nonce := u.frame, u.frameNonce
	pads := make([]any, len(u.sceneDown))
	for i, down := range u.sceneDown {
		pads[i] = down
	}
	u.frameSeq++
	seq := u.frameSeq
	u.sceneMu.Unlock()
	window := frame.Get("contentWindow")
	if !workspacePresent(window) {
		return
	}
	origin := js.Global().Get("location").Get("origin").String()
	window.Call("postMessage", map[string]any{"protocol": "cicada-sampler-frame-v1", "nonce": nonce, "type": "state", "seq": seq, "pads": pads}, origin)
}

// A focus transfer into our own same-origin frame is not a window deactivation.
// Fail closed when frame access cannot be verified.
func samplerFrameOwnsFocus() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	document := js.Global().Get("document")
	if !document.Call("hasFocus").Bool() {
		return false
	}
	frame := document.Call("querySelector", "[data-sampler-scene-container] iframe")
	if !workspacePresent(frame) || !document.Get("activeElement").Equal(frame) {
		return false
	}
	return frame.Get("contentWindow").Get("location").Get("origin").String() == js.Global().Get("location").Get("origin").String()
}
