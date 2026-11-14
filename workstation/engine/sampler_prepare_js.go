//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"syscall/js"
	"time"

	padsampler "m31labs.dev/cicada/host/padsampler/protocol"
)

// samplerLoadSnapshot is copied by the state owner before preparation starts.
// Prepared buffers remain isolated until a generation-checked state commit.
type samplerLoadSnapshot struct {
	audio   js.Value
	sounds  []string
	buffers map[string]js.Value
	initial bool
}

type samplerLoadResult struct {
	state   samplerProjection
	buffers map[string]js.Value
}

// snapshotSamplerLoad is called by the state executor, never by the worker.
func (u *samplerUI) snapshotSamplerLoad() samplerLoadSnapshot {
	snapshot := samplerLoadSnapshot{audio: u.audio, initial: !u.initialized, buffers: make(map[string]js.Value, len(u.buffers))}
	for id, buffer := range u.buffers {
		snapshot.buffers[id] = buffer
	}
	for _, hit := range u.perf.Hits {
		snapshot.sounds = append(snapshot.sounds, hit.Sound)
	}
	return snapshot
}

func prepareSamplerLoad(ctx context.Context, snapshot samplerLoadSnapshot) (samplerLoadResult, error) {
	var result samplerLoadResult
	if err := readNativeJSON(ctx, "/api/sampler", 15*time.Second, 2<<20, &result.state); err != nil {
		return result, err
	}
	if len(result.state.Sounds) != 16 {
		return result, fmt.Errorf("invalid pad bank")
	}
	result.buffers = make(map[string]js.Value)
	ids := append(append([]string(nil), result.state.Sounds...), snapshot.sounds...)
	if snapshot.initial {
		for _, hit := range result.state.Bank.Performance.Hits {
			ids = append(ids, hit.Sound)
		}
	}
	frames := 0
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := result.buffers[id]; ok {
			continue
		}
		buffer, ok := snapshot.buffers[id]
		if !ok {
			raw, err := samplerFetchAudio(ctx, "/media/sampler/"+id+".wav")
			if err != nil {
				return result, err
			}
			buffer, err = samplerDecodeAudio(ctx, snapshot.audio, raw)
			if err != nil {
				return result, err
			}
		}
		frames += buffer.Get("length").Int()
		if frames > padsampler.MaxFrames {
			return result, fmt.Errorf("pad monitor exceeds 64 MiB; shorten the loaded regions")
		}
		result.buffers[id] = buffer
	}
	return result, nil
}

var samplerDecodes samplerDecodeGate

// decodeAudioData cannot be cancelled. Retain at most one unresolved promise
// across all mounts, and do not retain a sampler UI in its callbacks.
func samplerDecodeAudio(ctx context.Context, audio, raw js.Value) (value js.Value, err error) {
	if !samplerDecodes.acquire() {
		return js.Undefined(), fmt.Errorf("a previous pad audio decode is still pending; reload the page to retry")
	}
	type outcome struct {
		value js.Value
		err   error
	}
	settled := make(chan outcome, 1)
	delivered := make(chan outcome, 1)
	ok := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			settled <- outcome{err: fmt.Errorf("pad audio decode returned no buffer")}
		} else {
			settled <- outcome{value: args[0]}
		}
		return nil
	})
	fail := js.FuncOf(func(_ js.Value, args []js.Value) any {
		message := "pad audio decode failed"
		if len(args) > 0 && args[0].Type() == js.TypeObject && !args[0].IsNull() && args[0].Get("message").Type() == js.TypeString {
			message = args[0].Get("message").String()
		}
		settled <- outcome{err: fmt.Errorf("%s", message)}
		return nil
	})
	defer func() {
		if recovered := recover(); recovered != nil {
			ok.Release()
			fail.Release()
			samplerDecodes.release()
			value, err = js.Undefined(), fmt.Errorf("pad audio decode failed: %v", recovered)
		}
	}()
	audio.Call("decodeAudioData", raw).Call("then", ok).Call("catch", fail)
	go func() {
		r := <-settled
		ok.Release()
		fail.Release()
		samplerDecodes.release()
		delivered <- r
	}()
	select {
	case r := <-delivered:
		return r.value, r.err
	case <-ctx.Done():
		return js.Undefined(), ctx.Err()
	}
}

func samplerFetchAudio(ctx context.Context, path string) (js.Value, error) {
	controller := js.Global().Get("AbortController").New()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			controller.Call("abort")
		case <-done:
		}
	}()
	defer close(done)
	response, err := awaitPromise(ctx, js.Global().Call("fetch", path, map[string]any{"signal": controller.Get("signal")}))
	if err != nil {
		return js.Undefined(), err
	}
	if !response.Get("ok").Bool() {
		return js.Undefined(), fmt.Errorf("prepared sound could not be loaded")
	}
	return awaitPromise(ctx, response.Call("arrayBuffer"))
}

type samplerResampleResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// postSamplerResample uses an immutable copy of the submitted form values.
func postSamplerResample(ctx context.Context, values map[string]string) (samplerResampleResult, error) {
	var result samplerResampleResult
	encoded, err := json.Marshal(values)
	if err != nil {
		return result, err
	}
	response, err := nativeRequest{Method: methodPost, Path: "/__actions/sampler", Body: encoded, Timeout: 30 * time.Second, Limit: 1 << 20, Headers: map[string]string{"Content-Type": "application/json", "Accept": "application/json", "X-Cicada-Reactive": "1", "X-CSRF-Token": values["csrf_token"]}}.do(ctx)
	if err != nil {
		return result, err
	}
	err = decodeLimited(response.Body, 1<<20, &result)
	return result, err
}

// commitSamplerLoad is called on the sampler state executor. Its guarded state
// assignment never yields; it preserves buffers needed by recordings made while
// preparation was in flight. DOM effects happen only after a successful commit.
func (u *samplerUI) commitSamplerLoad(loader *samplerLoader[samplerLoadResult], generation uint64, result samplerLoadResult, valid func() bool) bool {
	if !valid() {
		return false
	}
	for _, hit := range u.perf.Hits {
		if _, ok := result.buffers[hit.Sound]; !ok {
			if buffer, exists := u.buffers[hit.Sound]; exists {
				result.buffers[hit.Sound] = buffer
			}
		}
	}
	frames := 0
	for _, buffer := range result.buffers {
		frames += buffer.Get("length").Int()
		if !valid() {
			return false
		}
		if frames > padsampler.MaxFrames {
			u.status("Pad monitor exceeds 64 MiB; shorten the loaded regions. Your performance is retained.")
			return false
		}
	}
	changed, initialized := false, false
	if !loader.commit(generation, func() {
		changed = result.state.BankRevision != u.state.BankRevision
		u.state = result.state
		if !u.initialized {
			u.perf = result.state.Bank.Performance
			u.savedPerf = u.perfFingerprint()
			u.recordRevision = result.state.Revision
			u.initialized, initialized = true, true
		}
		u.buffers = result.buffers
	}) {
		return false
	}
	if initialized && valid() {
		u.events()
	}
	for _, key := range []string{"revision", "bankRevision"} {
		if !valid() {
			return false
		}
		value := result.state.Revision
		if key == "bankRevision" {
			value = result.state.BankRevision
		}
		nodes := u.root.Call("querySelectorAll", "input[name="+key+"]")
		for i := 0; i < nodes.Length() && valid(); i++ {
			nodes.Index(i).Set("value", value)
		}
	}
	if !valid() {
		return false
	}
	location, _ := url.Parse(workspaceLocation())
	selected, _ := strconv.Atoi(location.Query().Get("pad"))
	if selected < 1 || selected > 16 {
		selected = 1
	}
	for i := 0; i < 16 && valid(); i++ {
		button := u.root.Call("querySelector", fmt.Sprintf("[data-sampler-pad='%d']", i+1))
		button.Call("closest", ".sampler-pad").Call("setAttribute", "data-pad-selected", strconv.FormatBool(i+1 == selected))
		p := result.state.Bank.Pads[i]
		button.Call("querySelector", "strong").Set("textContent", p.Name)
		sub := "Empty"
		if p.Source != "" {
			sub = fmt.Sprintf("%+d st · %s", p.Pitch, p.Mode)
		}
		button.Call("querySelector", "small").Set("textContent", sub)
		_, prepared := u.buffers[result.state.Sounds[i]]
		button.Set("disabled", p.Source == "" || !prepared)
		button.Set("title", p.Name)
	}
	if !valid() {
		return false
	}
	if preview := u.root.Call("querySelector", "audio"); !preview.IsNull() && result.state.Sounds[selected-1] != "" {
		path := "/media/sampler/" + result.state.Sounds[selected-1] + ".wav"
		if preview.Call("getAttribute", "src").String() != path {
			preview.Call("setAttribute", "src", path)
		}
	}
	if !valid() {
		return false
	}
	u.draw()
	u.root.Call("setAttribute", "data-sampler-bank-revision", result.state.BankRevision)
	u.root.Call("setAttribute", "data-sampler-ready", "true")
	if changed && !u.recording {
		u.status("Pads ready · browser audio · record a performance, then resample it into Session.")
	}
	return true
}

type samplerLoadDelivery struct {
	generation uint64
	result     samplerLoadResult
	err        error
}

// initSamplerLoader is called before the executor is enabled. Its worker owns
// only copied snapshots and the bounded result mailbox.
func (u *samplerUI) initSamplerLoader() {
	u.loader = newSamplerLoader(u.ctx, func(ctx context.Context, _ uint64) (samplerLoadResult, error) {
		u.loadSnapshotMu.Lock()
		snapshot := u.loadSnapshot
		u.loadSnapshotMu.Unlock()
		return prepareSamplerLoad(ctx, snapshot)
	}, func(generation uint64, result samplerLoadResult, err error) {
		if epoch, notify := u.loadMailbox.put(samplerLoadDelivery{generation: generation, result: result, err: err}); notify {
			go u.notifySamplerLoad(epoch)
		}
	})
}

// notifySamplerLoad retries after fail-closed overflow rather than losing the
// only notification for a prepared result. At most one notifier is active.
func (u *samplerUI) notifySamplerLoad(epoch uint64) {
	for !u.closed.Load() && u.loadMailbox.current(epoch) {
		if u.tryDispatchSampler(samplerCommand{kind: "refresh-result"}) {
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

func (u *samplerUI) requestSamplerLoad(invalidate bool) {
	snapshot := u.snapshotSamplerLoad()
	u.loadSnapshotMu.Lock()
	u.loadSnapshot = snapshot
	u.loadSnapshotMu.Unlock()
	u.loader.request(invalidate)
}

func (u *samplerUI) finishSamplerLoad() {
	delivery, ok := u.loadMailbox.take()
	if !ok {
		return
	}
	if !u.loader.current(delivery.generation) {
		return
	}
	if delivery.err != nil {
		u.status("Could not prepare pads: " + delivery.err.Error())
		return
	}
	if u.commitSamplerLoad(u.loader, delivery.generation, delivery.result, u.samplerEffectsAllowed) && u.resetRecordRevision {
		u.recordRevision = u.state.Revision
		u.resetRecordRevision = false
	}
}
