//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strings"
	"syscall/js"
	"testing"
	"time"

	padsampler "m31labs.dev/cicada/host/padsampler/protocol"
	"m31labs.dev/gosx/signal"
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
	oldBuffer := js.ValueOf(map[string]any{"length": padsampler.MaxFrames/2 + 1})
	newBuffer := js.ValueOf(map[string]any{"length": padsampler.MaxFrames/2 + 1})
	kept, trimmed, ok := fitSamplerBuffers(map[string]js.Value{"recorded": oldBuffer, "new": newBuffer}, []string{"recorded"}, []string{"new"}, func() bool { return true })
	if !ok || !trimmed || len(kept) != 1 || !kept["recorded"].Equal(oldBuffer) {
		t.Fatal("budget selection did not preserve recorded audio ahead of the new pad")
	}
}

func TestSamplerPrepareKeepsValidPadWhenAnotherFails(t *testing.T) {
	good := js.ValueOf(map[string]any{"length": 128})
	for _, message := range []string{"HTTP 404", "decode failed"} {
		buffers, warning, err := prepareSamplerBuffers(context.Background(), []string{"broken", "working"}, nil, func(_ context.Context, id string) (js.Value, error) {
			if id == "broken" {
				return js.Undefined(), fmt.Errorf("%s", message)
			}
			return good, nil
		})
		if err != nil || !strings.Contains(warning, message) || len(buffers) != 1 || !buffers["working"].Equal(good) {
			t.Fatalf("failure %q lost the other pad: buffers=%d warning=%q err=%v", message, len(buffers), warning, err)
		}
	}
}

func TestSamplerPrepareKeepsEarlierPadWhenDecodeNeverSettles(t *testing.T) {
	good := js.ValueOf(map[string]any{"length": 128})
	audio := js.Global().Call("eval", `({resolve: null, decodeAudioData() { return new Promise(resolve => { this.resolve = resolve; }); }})`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	buffers, warning, err := prepareSamplerBuffers(ctx, []string{"stalled", "working"}, map[string]js.Value{"working": good}, func(ctx context.Context, _ string) (js.Value, error) {
		return samplerDecodeAudio(ctx, audio, js.Null())
	})
	if err != nil || len(buffers) != 1 || !buffers["working"].Equal(good) || !strings.Contains(warning, "reload") {
		t.Fatalf("stalled decode lost earlier pad: buffers=%d warning=%q err=%v", len(buffers), warning, err)
	}
	audio.Call("resolve", js.ValueOf(map[string]any{"length": 1}))
	deadline := time.Now().Add(time.Second)
	for !samplerDecodes.acquire() {
		if time.Now().After(deadline) {
			t.Fatal("settled test decode did not release the slot")
		}
		time.Sleep(time.Millisecond)
	}
	samplerDecodes.release()
}

func TestSamplerPrepareCancellationDiscardsPartialGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	good := js.ValueOf(map[string]any{"length": 128})
	buffers, _, err := prepareSamplerBuffers(ctx, []string{"working", "cancelled"}, map[string]js.Value{"working": good}, func(context.Context, string) (js.Value, error) {
		cancel()
		return js.Undefined(), context.Canceled
	})
	if err != context.Canceled || buffers != nil {
		t.Fatalf("cancelled generation retained partial data: buffers=%v err=%v", buffers, err)
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

func TestSamplerRefreshDoesNotReplaceNewRecordingRevision(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		stop         bool
	}{
		{name: "recording with source revision", source: "project-at-record"},
		{name: "stopped recording with empty source revision", stop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := js.Global().Call("eval", `(() => {
				const field={value:""}, status={}, strip={replaceChildren(){}};
				const form={querySelector(){return field}};
				return {field, querySelector(s){if(s==='input[name=recordBars]')return {value:'1'};if(s==='form')return form;if(s==='[data-sampler-events]')return strip;if(s==='input[name=performance]')return {value:''};if(s==='[data-sampler-status]')return status;return null},setAttribute(){}};
			})()`)
			root.Get("field").Set("value", tc.source)
			u := &samplerUI{root: root, audio: js.ValueOf(map[string]any{"currentTime": 0}), state: samplerProjection{Revision: "score-at-record", Tempo: 120000}, resetRecordRevision: true}
			u.record()
			if !u.recording {
				t.Fatal("recording did not start")
			}
			if tc.stop {
				u.stop()
			}
			u.state.Revision = "later-refresh"
			u.finishRecordRevisionRefresh()
			if u.recordRevision != "score-at-record" || u.recordSourceRevision != tc.source {
				t.Fatalf("refresh changed the new recording receipt: score=%q source=%q", u.recordRevision, u.recordSourceRevision)
			}
		})
	}
}

func TestSamplerRefreshUpdatesRevisionWithoutNewRecording(t *testing.T) {
	u := &samplerUI{state: samplerProjection{Revision: "refreshed"}, recordRevision: "old", resetRecordRevision: true}
	u.finishRecordRevisionRefresh()
	if u.recordRevision != "refreshed" || u.resetRecordRevision {
		t.Fatal("post-resample refresh did not update the prior performance revision")
	}
}

func TestSamplerRefreshSubscriberEnqueueDoesNotResignal(t *testing.T) {
	var x *samplerExecutor
	u := &samplerUI{}
	done := make(chan struct{})
	seen := workspaceRefreshRequested.Get()
	calls, processed := 0, false
	effect := signal.Watch(func() {
		current := workspaceRefreshRequested.Get()
		if current != seen {
			seen = current
			calls++
			x.submit(samplerCommand{kind: "new-edit", dirties: true})
		}
	})
	defer effect.Dispose()
	x = newSamplerExecutor(4, func(c samplerCommand, _ func() bool) {
		if c.kind == "save" {
			u.workspaceRefreshPending = true
		} else {
			processed = true
		}
		x.publishDirty(false)
	}, func() bool { return false })
	u.executor = x
	x.after = func() {
		u.flushSamplerWorkspaceRefresh()
		if processed {
			close(done)
		}
	}
	x.submit(samplerCommand{kind: "save", dirties: true})
	samplerTestWait(t, done)
	if calls != 1 || u.workspaceRefreshPending {
		t.Fatalf("got %d refresh signals; pending=%t", calls, u.workspaceRefreshPending)
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
