//go:build js && wasm

package main

import (
	"context"
	"strings"
	"syscall/js"
	"testing"
	"time"

	padsampler "m31labs.dev/cicada/host/padsampler/protocol"
)

func TestSamplerDecodeSynchronousThrowReleasesSlot(t *testing.T) {
	audio := js.Global().Call("eval", `({decodeAudioData() { throw new Error("closed"); }})`)
	if _, err := samplerDecodeAudio(context.Background(), audio, js.Null()); err == nil {
		t.Fatal("decode throw not reported")
	}
	if !samplerDecodes.acquire() {
		t.Fatal("decode throw leaked slot")
	}
	samplerDecodes.release()
}

func TestSamplerLoadCommitPreservesRecordingWhenUnionExceedsBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loader := newSamplerLoader(ctx, func(context.Context, uint64) (samplerLoadResult, error) { return samplerLoadResult{}, nil }, func(uint64, samplerLoadResult, error) {})
	oldBuffer := js.ValueOf(map[string]any{"length": padsampler.MaxFrames/2 + 1})
	newBuffer := js.ValueOf(map[string]any{"length": padsampler.MaxFrames/2 + 1})
	root := js.Global().Call("eval", `({querySelector() { return {}; }})`)
	u := &samplerUI{root: root, initialized: true, state: samplerProjection{BankRevision: "old"}, buffers: map[string]js.Value{"recorded": oldBuffer}, perf: padsampler.Performance{Hits: []padsampler.Hit{{Sound: "recorded"}}}}
	result := samplerLoadResult{state: samplerProjection{BankRevision: "new"}, buffers: map[string]js.Value{"new": newBuffer}}
	if u.commitSamplerLoad(loader, 0, result, func() bool { return true }) {
		t.Fatal("committed buffers beyond the limit")
	}
	if u.state.BankRevision != "old" || len(u.perf.Hits) != 1 || len(u.buffers) != 1 {
		t.Fatal("failed commit changed the current bank or recording")
	}
}

func TestSamplerDelayedFrameKeyupDoesNotReleaseNewParentKey(t *testing.T) {
	previous := js.Global().Get("location")
	js.Global().Set("location", js.ValueOf(map[string]any{"origin": "http://localhost"}))
	defer js.Global().Set("location", previous)
	window := js.ValueOf(map[string]any{})
	frame := js.ValueOf(map[string]any{"contentWindow": window})
	u := &samplerUI{frame: frame, frameNonce: "frame-1", inputKeys: map[int]uint64{1: 1}, keyPads: map[int]bool{1: true}, padOwners: map[int]map[string]bool{1: {"key:1": true}}}
	event := js.ValueOf(map[string]any{"origin": "http://localhost", "source": window, "data": map[string]any{"protocol": "cicada-sampler-frame-v1", "nonce": "frame-1", "type": "keyup", "pad": 1}})
	u.sceneMessage(event)
	if u.inputKeys[1] == 0 || !u.keyPads[1] || !u.padOwners[1]["key:1"] {
		t.Fatal("delayed frame keyup released a newer parent press")
	}
}

func TestSamplerFrameFocusReleasesOnlyKeyboardHolds(t *testing.T) {
	u := &samplerUI{
		audio:     js.ValueOf(map[string]any{"currentTime": 0}),
		inputKeys: map[int]uint64{1: 1}, keyPads: map[int]bool{1: true},
		heldPads:  map[int]bool{1: true, 2: true, 3: true},
		padOwners: map[int]map[string]bool{1: {"key:1": true}, 2: {"pointer:7": true}, 3: {"midi:1": true}},
	}
	u.samplerFrameFocusTransfer()
	if u.inputKeys[1] != 0 || u.keyPads[1] || u.padOwners[1]["key:1"] {
		t.Fatal("keyboard hold survived frame focus transfer")
	}
	if !u.heldPads[2] || !u.padOwners[2]["pointer:7"] {
		t.Fatal("frame focus transfer released a pointer hold")
	}
	if !u.heldPads[3] || !u.padOwners[3]["midi:1"] {
		t.Fatal("frame focus transfer released a MIDI hold")
	}
}

func TestSamplerWaveCaptureRequiresEligibleSnapshot(t *testing.T) {
	u := &samplerUI{}
	if u.samplerWaveMayCapture(0, true) {
		t.Fatal("unprepared or posting waveform captured input")
	}
	u.waveEligible.Store(true)
	if !u.samplerWaveMayCapture(0, true) || u.samplerWaveMayCapture(1, true) || u.samplerWaveMayCapture(0, false) {
		t.Fatal("primary pointer eligibility incorrect")
	}
	u.waveInputActive = true
	if u.samplerWaveMayCapture(0, true) {
		t.Fatal("second pointer captured an active gesture")
	}
}

func TestSamplerExecutorReentrantJSOverflowReleasesOwner(t *testing.T) {
	cleaned := make(chan struct{})
	owner := false
	var x *samplerExecutor
	callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		x.submit(samplerCommand{kind: "queued"})
		x.submit(samplerCommand{kind: "overflow"})
		return nil
	})
	defer callback.Release()
	x = newSamplerExecutor(1, func(c samplerCommand, valid func() bool) {
		if c.kind != "start" {
			return
		}
		owner = true
		callback.Invoke()
		if valid() {
			owner = true
		}
	}, func() bool {
		owner = false
		select {
		case <-cleaned:
		default:
			close(cleaned)
		}
		return false
	})
	if !x.submit(samplerCommand{kind: "start"}) {
		t.Fatal("start rejected")
	}
	samplerTestWait(t, cleaned)
	if owner {
		t.Fatal("revoked owner survived cleanup")
	}
	x.close()
}

func TestSamplerReentrantStartDisposesTrackedVoice(t *testing.T) {
	cleaned := make(chan struct{})
	var x *samplerExecutor
	callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		x.submit(samplerCommand{kind: "queued"})
		x.submit(samplerCommand{kind: "overflow"})
		return nil
	})
	defer callback.Release()
	js.Global().Set("samplerTestReenter", callback)
	defer js.Global().Delete("samplerTestReenter")
	fixture := js.Global().Call("eval", `(() => {
		const source = {stopped: false, disconnected: false, connect(){}, start(){samplerTestReenter()}, stop(){this.stopped = true}, disconnect(){this.disconnected = true}};
		const gain = {disconnected: false, gain: {setValueAtTime(){}}, connect(){}, disconnect(){this.disconnected = true}};
		return {source, gain, audio: {destination: {}, createBufferSource(){return source}, createGain(){return gain}}, root: {querySelector(){return {setAttribute(){}}}}};
	})()`)
	u := &samplerUI{audio: fixture.Get("audio"), root: fixture.Get("root"), padOwners: map[int]map[string]bool{1: {"frame:old": true}}}
	buffer := js.ValueOf(map[string]any{"duration": 1})
	x = newSamplerExecutor(1, func(c samplerCommand, valid func() bool) {
		if c.kind == "start" {
			u.effectValid = valid
			defer func() { u.effectValid = nil }()
			if u.startVoice(buffer, 1, 0, "gate", 1, 0, -1) != nil {
				t.Error("revoked voice start returned success")
			}
		}
	}, func() bool {
		u.padOwners = make(map[int]map[string]bool)
		u.disposeSamplerVoices()
		select {
		case <-cleaned:
		default:
			close(cleaned)
		}
		return false
	})
	if !x.submit(samplerCommand{kind: "start"}) {
		t.Fatal("start rejected")
	}
	samplerTestWait(t, cleaned)
	if !fixture.Get("source").Get("stopped").Bool() || !fixture.Get("source").Get("disconnected").Bool() || !fixture.Get("gain").Get("disconnected").Bool() || len(u.voices) != 0 || len(u.padOwners) != 0 {
		t.Fatal("reentrant overflow left audio or ownership active")
	}
	x.close()
}

func TestSamplerTeardownDuringStartStopsAgainAfterReturn(t *testing.T) {
	var u *samplerUI
	callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		u.closed.Store(true)
		u.disposeSamplerVoices()
		return nil
	})
	defer callback.Release()
	js.Global().Set("samplerTestTeardown", callback)
	defer js.Global().Delete("samplerTestTeardown")
	fixture := js.Global().Call("eval", `(() => {
		const source = {active: false, stops: 0, disconnected: false, connect(){}, start(){samplerTestTeardown(); this.active = true}, stop(){this.active = false; this.stops++}, disconnect(){this.disconnected = true}};
		const gain = {gain: {setValueAtTime(){}}, connect(){}, disconnect(){}};
		return {source, audio: {destination: {}, createBufferSource(){return source}, createGain(){return gain}}};
	})()`)
	u = &samplerUI{audio: fixture.Get("audio")}
	if u.startVoice(js.ValueOf(map[string]any{"duration": 1}), 1, 0, "gate", 1, 0, -1) != nil {
		t.Fatal("start succeeded after reentrant teardown")
	}
	source := fixture.Get("source")
	if source.Get("active").Bool() || !source.Get("disconnected").Bool() || source.Get("stops").Int() < 2 {
		t.Fatal("source survived reentrant teardown")
	}
}

func TestSamplerOverflowBeforeResamplePostDoesNotLeavePostingLocked(t *testing.T) {
	cleaned := make(chan struct{})
	var x *samplerExecutor
	callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		x.submit(samplerCommand{kind: "queued"})
		x.submit(samplerCommand{kind: "overflow"})
		return nil
	})
	defer callback.Release()
	js.Global().Set("samplerTestResampleOverflow", callback)
	defer js.Global().Delete("samplerTestResampleOverflow")
	root := js.Global().Call("eval", `(() => { const status={set textContent(v){samplerTestResampleOverflow()}}; return {querySelector(s){return s.includes('status') ? status : null},setAttribute(){}}; })()`)
	u := &samplerUI{root: root, audio: js.ValueOf(map[string]any{"currentTime": 0}), perf: padsampler.Performance{Hits: []padsampler.Hit{{Sound: "kept"}}}}
	x = newSamplerExecutor(1, func(c samplerCommand, valid func() bool) {
		u.effectValid = valid
		defer func() { u.effectValid = nil }()
		u.resample(`{"place":""}`)
	}, func() bool {
		dirty := u.samplerCleanup()
		select {
		case <-cleaned:
		default:
			close(cleaned)
		}
		return dirty
	})
	if !x.submit(samplerCommand{kind: "resample"}) {
		t.Fatal("resample rejected")
	}
	samplerTestWait(t, cleaned)
	if u.posting || u.postInFlight || len(u.perf.Hits) != 1 {
		t.Fatal("pre-send overflow left posting locked or discarded the performance")
	}
	x.close()
}

func TestSamplerSynchronousNestedJSEvent(t *testing.T) {
	target := js.Global().Get("EventTarget").New()
	seen := false
	inner := js.FuncOf(func(_ js.Value, _ []js.Value) any { seen = true; return nil })
	defer inner.Release()
	target.Call("addEventListener", "sampler-pick", inner)
	outer := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		target.Call("dispatchEvent", js.Global().Get("Event").New("sampler-pick"))
		return nil
	})
	defer outer.Release()
	outer.Invoke()
	if !seen {
		t.Fatal("synchronous nested pick event was not delivered")
	}
}

func samplerTestWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("sampler cleanup timed out")
	}
}

func TestSamplerDecodeNeverSettlesRemainsBounded(t *testing.T) {
	audio := js.Global().Call("eval", `({resolve: null, decodeAudioData() { return new Promise(resolve => { this.resolve = resolve; }); }})`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := samplerDecodeAudio(ctx, audio, js.Null()); err == nil {
		t.Fatal("cancelled wait succeeded")
	}
	for i := 0; i < 100; i++ {
		if _, err := samplerDecodeAudio(ctx, audio, js.Null()); err == nil || !strings.Contains(err.Error(), "reload") {
			t.Fatalf("pending decode not reported: %v", err)
		}
	}
	audio.Call("resolve", js.ValueOf(map[string]any{"length": 1}))
	deadline := time.Now().Add(time.Second)
	for !samplerDecodes.acquire() {
		if time.Now().After(deadline) {
			t.Fatal("settled decode did not release the slot")
		}
		time.Sleep(time.Millisecond)
	}
	samplerDecodes.release()
}
