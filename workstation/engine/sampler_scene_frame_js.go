//go:build js && wasm

package main

import (
	"context"
	"fmt"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	"m31labs.dev/gosx/scene"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"
)

func mountSamplerFrame(host enginewasm.Context) (enginewasm.Handle, error) {
	root := host.Mount()
	mount := root.Call("querySelector", "#sampler-frame-scene")
	if mount.IsNull() {
		return nil, fmt.Errorf("sampler scene unavailable")
	}
	window := js.Global()
	parent := window.Get("parent")
	origin := window.Get("location").Get("origin").String()
	nonce := strings.TrimPrefix(window.Get("location").Get("hash").String(), "#")
	if len(nonce) != 36 || parent.Equal(window) {
		return nil, fmt.Errorf("sampler frame requires parent and nonce")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var closed atomic.Bool
	type listener struct {
		node    js.Value
		event   string
		fn      js.Func
		capture bool
	}
	var listeners []listener
	listen := func(node js.Value, event string, capture bool, fn func(js.Value)) {
		f := js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) > 0 {
				fn(a[0])
			}
			return nil
		})
		node.Call("addEventListener", event, f, capture)
		listeners = append(listeners, listener{node, event, f, capture})
	}
	send := func(kind string, pad, pointer int) {
		parent.Call("postMessage", map[string]any{"protocol": "cicada-sampler-frame-v1", "nonce": nonce, "type": kind, "pad": pad, "pointerId": pointer}, origin)
	}
	// The frame's declared GoSX input controller loads the optional chunk
	// through the document contract and relays correlated ray results. If it is
	// unavailable, retain the native single-pointer picking path.
	var rayReady atomic.Bool
	controllerReady := func() bool {
		defer func() { _ = recover() }()
		return window.Get("__gosx").Get("host").Get("controllers").Get("pickScene").Type() == js.TypeFunction
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; i < 50; i++ {
			if closed.Load() || ctx.Err() != nil {
				return
			}
			if controllerReady() {
				rayReady.Store(true)
				root.Call("setAttribute", "data-sampler-multipointer", "ready")
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		if !closed.Load() {
			root.Call("setAttribute", "data-sampler-multipointer", "fallback")
		}
	}()

	owners := make(map[int]int)
	pending := make(map[string]int)
	sequence := uint64(0)
	fallbackOwner, fallbackPad := -1, 0
	listen(mount, "pointerdown", true, func(e js.Value) {
		target := e.Get("target")
		if !workspacePresent(target.Call("closest", "canvas")) || e.Get("button").Int() != 0 {
			return
		}
		pointer := e.Get("pointerId").Int()
		if _, exists := owners[pointer]; exists {
			return
		}
		if rayReady.Load() {
			sequence++
			requestID := fmt.Sprintf("sampler:%d", sequence)
			pending[requestID] = -1
			detail := map[string]any{"requestId": requestID, "pointerId": pointer, "clientX": e.Get("clientX").Float(), "clientY": e.Get("clientY").Float()}
			mount.Call("dispatchEvent", window.Get("CustomEvent").New("gosx:scene3d:pick-request", map[string]any{"detail": detail}))
			pad := pending[requestID]
			delete(pending, requestID)
			if pad < 0 {
				if fallbackOwner != -1 {
					e.Call("preventDefault")
					e.Call("stopImmediatePropagation")
					return
				}
				fallbackOwner, fallbackPad = pointer, 0
				return
			}
			if pad == 0 {
				return
			} // A reported miss remains available to orbit.
			if len(owners) >= 32 {
				e.Call("preventDefault")
				e.Call("stopImmediatePropagation")
				return
			}
			if e.Get("isTrusted").Bool() && !samplerFrameCapture(target, pointer) {
				return
			}
			owners[pointer] = pad
			e.Call("preventDefault")
			e.Call("stopImmediatePropagation")
			send("down", pad, pointer)
			return
		}
		if fallbackOwner != -1 {
			e.Call("preventDefault")
			e.Call("stopImmediatePropagation")
			return
		}
		fallbackOwner, fallbackPad = pointer, 0
	})
	listen(mount, "cicada:sampler-ray", false, func(e js.Value) {
		detail := e.Get("detail")
		if detail.Type() != js.TypeObject || detail.IsNull() {
			return
		}
		input := detail.Get("input")
		if input.Type() != js.TypeObject || input.IsNull() {
			return
		}
		requestID := workspaceString(input, "requestId")
		if _, exists := pending[requestID]; !exists {
			return
		}
		pending[requestID] = 0
		hit := input.Get("hit")
		if hit.Type() == js.TypeObject && !hit.IsNull() {
			pending[requestID] = samplerFramePad(workspaceString(hit, "id"))
		}
	})
	listen(mount, "gosx:scene3d:input", false, func(e js.Value) {
		detail := e.Get("detail")
		if detail.Type() != js.TypeObject || detail.IsNull() {
			return
		}
		input := detail.Get("input")
		if input.Type() != js.TypeObject || input.IsNull() {
			return
		}
		if fallbackOwner == -1 || fallbackPad != 0 || workspaceString(input, "type") != "down" {
			return
		}
		pad := samplerFramePad(workspaceString(input, "targetID"))
		if pad == 0 {
			return
		}
		fallbackPad = pad
		owners[fallbackOwner] = pad
		send("down", pad, fallbackOwner)
	})

	finish := func(e js.Value) {
		pointer := e.Get("pointerId").Int()
		if e.Get("type").String() == "lostpointercapture" {
			target := e.Get("target")
			if workspacePresent(target) && target.Get("hasPointerCapture").Type() == js.TypeFunction && target.Call("hasPointerCapture", pointer).Bool() {
				return
			}
		}
		if pad, exists := owners[pointer]; exists {
			delete(owners, pointer)
			send("up", pad, pointer)
		}
		if fallbackOwner == pointer {
			fallbackOwner, fallbackPad = -1, 0
		}
	}
	for _, event := range []string{"pointerup", "pointercancel", "lostpointercapture"} {
		listen(window.Get("document"), event, true, finish)
	}
	releaseAll := func() {
		for pointer, pad := range owners {
			delete(owners, pointer)
			send("up", pad, pointer)
		}
		fallbackOwner, fallbackPad = -1, 0
	}
	listen(window, "pagehide", false, func(js.Value) { send("dispose", 0, 0) })
	listen(window, "blur", false, func(js.Value) { releaseAll() })
	listen(window.Get("document"), "visibilitychange", false, func(js.Value) {
		if window.Get("document").Get("hidden").Bool() {
			releaseAll()
		}
	})

	var revision int
	var mu sync.Mutex
	var states [16]bool
	wake := make(chan struct{}, 1)
	target := host.Scene3D(mount)
	listen(window, "message", false, func(e js.Value) {
		if e.Get("origin").String() != origin || !e.Get("source").Equal(parent) {
			return
		}
		d := e.Get("data")
		if (d.Type() != js.TypeObject || d.IsNull()) || workspaceString(d, "protocol") != "cicada-sampler-frame-v1" || workspaceString(d, "nonce") != nonce || workspaceString(d, "type") != "state" {
			return
		}
		seq, values := d.Get("seq"), d.Get("pads")
		if seq.Type() != js.TypeNumber || !window.Get("Array").Call("isArray", values).Bool() || values.Length() != 16 {
			return
		}
		nf := seq.Float()
		if math.IsNaN(nf) || math.IsInf(nf, 0) || nf != math.Trunc(nf) || nf < 1 || nf > 2147483647 {
			return
		}
		var next [16]bool
		for i := range next {
			v := values.Index(i)
			if v.Type() != js.TypeBoolean {
				return
			}
			next[i] = v.Bool()
		}
		n := int(nf)
		if n <= revision {
			return
		}
		revision = n
		mu.Lock()
		states = next
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	go func() {
		if target.Ready(ctx) != nil || closed.Load() {
			return
		}
		send("ready", 0, 0)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !closed.Load() {
						send("heartbeat", 0, 0)
					}
				}
			}
		}()
		select {
		case wake <- struct{}{}:
		default:
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				mu.Lock()
				snapshot := states
				mu.Unlock()
				commands := make([]scene.Command, 0, 32)
				for i := range snapshot {
					commands = append(commands, samplerSceneCommand(i+1, snapshot[i]), samplerSceneLightCommand(i+1, snapshot[i]))
				}
				_ = target.Dispatch(ctx, commands)
			}
		}
	}()
	return enginewasm.HandleFunc(func() {
		closed.Store(true)
		send("dispose", 0, 0)
		cancel()
		for _, l := range listeners {
			l.node.Call("removeEventListener", l.event, l.fn, l.capture)
			l.fn.Release()
		}
	}), nil
}

func samplerFramePad(id string) int {
	if !strings.HasPrefix(id, "pad-") {
		return 0
	}
	pad, err := strconv.Atoi(strings.TrimPrefix(id, "pad-"))
	if err != nil || pad < 1 || pad > 16 {
		return 0
	}
	return pad
}

func samplerFrameCapture(target js.Value, pointer int) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	target.Call("setPointerCapture", pointer)
	return true
}
