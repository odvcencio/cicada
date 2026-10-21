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
	"sync"
	"sync/atomic"
	"syscall/js"
	"time"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

type soundJob struct {
	patch, name string
	sequence    uint64
}

type soundsUI struct {
	root  js.Value
	props struct {
		CSRF, Scale string
		Tempo, Root int
	}
	ctx      context.Context
	cancel   context.CancelFunc
	closed   atomic.Bool
	lease    string
	renew    time.Time
	sequence uint64
	selected string
	pending  bool
	wake     chan struct{}
	jobMu    sync.Mutex
	latest   soundJob
	bindings []samplerBinding
}

type soundResponse struct {
	Lease      string `json:"lease"`
	ExpiresMS  int64  `json:"expiresMs"`
	DurationMS int64  `json:"durationMs"`
	Active     bool   `json:"active"`
	Error      string `json:"error"`
}

func mountSounds(host enginewasm.Context) (enginewasm.Handle, error) {
	ctx, cancel := context.WithCancel(context.Background())
	u := &soundsUI{root: host.Mount(), ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	if err := host.DecodeProps(&u.props); err != nil {
		cancel()
		return nil, err
	}
	if !workspacePresent(u.root) {
		cancel()
		return nil, fmt.Errorf("sound browser is missing")
	}
	u.on(u.root, "click", func(e js.Value) {
		button := e.Get("target").Call("closest", "[data-sound-preview], [data-sound-stop]")
		if !workspacePresent(button) || !u.root.Call("contains", button).Bool() {
			return
		}
		e.Call("preventDefault")
		patch := workspaceAttr(button, "data-sound-preview")
		if patch == "" || patch == u.selected {
			u.stop("Preview stopped.")
			return
		}
		// Retire a pending preparation before sending the newer choice. The
		// server sequence guard also handles these requests arriving backwards.
		u.sequence++
		u.beacon(u.lease, "stop", u.sequence)
		u.sequence++
		u.selected, u.pending = patch, true
		name := workspaceAttr(button, "data-sound-name")
		u.render("Preparing " + name + "…")
		u.queue(soundJob{patch, name, u.sequence})
	})
	u.on(u.root, "keydown", func(e js.Value) {
		if key := e.Get("key").String(); key == " " || key == "Enter" {
			if workspacePresent(e.Get("target").Call("closest", "[data-sound-preview], [data-sound-stop]")) {
				e.Call("stopPropagation")
			}
		}
		if e.Get("key").String() == "Escape" && u.selected != "" {
			e.Call("preventDefault")
			e.Call("stopPropagation")
			u.stop("Preview stopped.")
		}
	})
	u.on(js.Global(), "pagehide", func(js.Value) {
		u.sequence++
		u.beacon(u.lease, "close", u.sequence)
	})
	u.root.Call("setAttribute", "data-sounds-ready", "true")
	u.render("Listen to a sound, then add it to your song.")
	go u.run()
	go u.observe()
	return enginewasm.HandleFunc(func() {
		if u.closed.Swap(true) {
			return
		}
		u.sequence++
		u.beacon(u.lease, "close", u.sequence)
		u.cancel()
		for _, binding := range u.bindings {
			binding.node.Call("removeEventListener", binding.event, binding.fn)
			binding.fn.Release()
		}
	}), nil
}

func (u *soundsUI) on(node js.Value, event string, handler func(js.Value)) {
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && !u.closed.Load() {
			handler(args[0])
		}
		return nil
	})
	node.Call("addEventListener", event, callback)
	u.bindings = append(u.bindings, samplerBinding{node, event, callback})
}

func (u *soundsUI) render(message string) {
	if u.closed.Load() || !u.root.Get("isConnected").Bool() {
		return
	}
	u.root.Call("querySelector", "[data-sound-status]").Set("textContent", message)
	buttons := u.root.Call("querySelectorAll", "[data-sound-preview]")
	for i := 0; i < buttons.Length(); i++ {
		button := buttons.Index(i)
		selected := u.selected == workspaceAttr(button, "data-sound-preview")
		label, text := "Listen to "+workspaceAttr(button, "data-sound-name"), "Listen"
		if selected {
			label, text = "Stop "+workspaceAttr(button, "data-sound-name")+" preview", "Stop"
			if u.pending {
				text = "Cancel"
			}
		}
		button.Set("disabled", false)
		button.Set("textContent", text)
		button.Call("setAttribute", "aria-label", label)
		button.Call("setAttribute", "aria-pressed", strconv.FormatBool(selected))
		button.Call("setAttribute", "aria-busy", strconv.FormatBool(selected && u.pending))
	}
	u.root.Call("querySelector", "[data-sound-stop]").Set("disabled", u.selected == "")
}

func (u *soundsUI) stop(message string) {
	u.sequence++
	u.beacon(u.lease, "stop", u.sequence)
	u.pending = true
	u.render("Stopping preview…")
	u.queue(soundJob{"", message, u.sequence})
}

func (u *soundsUI) queue(job soundJob) {
	u.jobMu.Lock()
	if job.sequence >= u.latest.sequence {
		u.latest = job
	}
	u.jobMu.Unlock()
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

func (u *soundsUI) beacon(lease, action string, sequence uint64) {
	if lease == "" {
		return
	}
	form := url.Values{"csrf_token": {u.props.CSRF}, "action": {action}, "lease": {lease}, "sequence": {strconv.FormatUint(sequence, 10)}}
	blob := js.Global().Get("Blob").New([]any{form.Encode()}, map[string]any{"type": "application/x-www-form-urlencoded"})
	js.Global().Get("navigator").Call("sendBeacon", "/__actions/sound-preview", blob)
}

func (u *soundsUI) post(ctx context.Context, values map[string]string) (soundResponse, error) {
	var result soundResponse
	values["csrf_token"] = u.props.CSRF
	data, err := json.Marshal(values)
	if err != nil {
		return result, err
	}
	origin := js.Global().Get("location").Get("origin").String()
	request, err := http.NewRequestWithContext(ctx, "POST", origin+"/__actions/sound-preview", bytes.NewReader(data))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-CSRF-Token", u.props.CSRF)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return result, fmt.Errorf("sound preview could not reach the audio service")
	}
	defer response.Body.Close()
	var receipt struct {
		OK      bool          `json:"ok"`
		Message string        `json:"message"`
		Data    soundResponse `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&receipt); err != nil {
		return result, fmt.Errorf("sound preview returned an unreadable response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !receipt.OK {
		if receipt.Message == "" {
			receipt.Message = "Sound preview is unavailable."
		}
		return result, fmt.Errorf("%s", receipt.Message)
	}
	return receipt.Data, nil
}

func (u *soundsUI) ensureLease() error {
	if u.lease != "" && time.Now().Before(u.renew) {
		return nil
	}
	u.beacon(u.lease, "close", max(1, u.sequence))
	// A managed navigation may dispose the surface during this handshake.
	// Read the response so that its new lease can still be released afterward.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := u.post(ctx, map[string]string{"action": "open"})
	if err != nil {
		return err
	}
	if u.closed.Load() {
		u.beacon(response.Lease, "close", max(1, u.sequence))
		return context.Canceled
	}
	u.lease, u.renew = response.Lease, time.Now().Add(time.Duration(response.ExpiresMS)*time.Millisecond-time.Minute)
	return nil
}

func (u *soundsUI) run() {
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-u.wake:
			u.jobMu.Lock()
			job := u.latest
			u.jobMu.Unlock()
			if job.sequence != u.sequence {
				continue
			}
			if job.patch == "" {
				var err error
				if u.lease != "" {
					_, err = u.post(u.ctx, map[string]string{"action": "stop", "lease": u.lease, "sequence": strconv.FormatUint(job.sequence, 10)})
				}
				if job.sequence == u.sequence && !u.closed.Load() {
					u.pending = false
					if err != nil {
						u.render(err.Error())
					} else {
						u.selected = ""
						u.render(job.name)
					}
				}
				continue
			}
			err := u.ensureLease()
			if job.sequence != u.sequence || u.closed.Load() {
				continue
			}
			if err == nil {
				_, err = u.post(u.ctx, map[string]string{"action": "play", "lease": u.lease, "sequence": strconv.FormatUint(job.sequence, 10), "patch": job.patch, "tempo": strconv.Itoa(u.props.Tempo), "root": strconv.Itoa(u.props.Root), "scale": u.props.Scale})
			}
			if job.sequence != u.sequence || u.closed.Load() {
				continue
			}
			u.pending = false
			if err != nil {
				// The service may have started audio before its acknowledgement
				// was lost. Retire that request and acknowledge Stop before
				// declaring the browser idle; a failed Stop remains retryable.
				u.stop(err.Error())
			} else {
				u.render("Listening to " + job.name + ".")
			}
		}
	}
}

func (u *soundsUI) observe() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			if u.selected == "" || u.pending || u.lease == "" {
				continue
			}
			sequence := u.sequence
			result, err := u.post(u.ctx, map[string]string{"action": "state", "lease": u.lease})
			if sequence != u.sequence || u.pending {
				continue
			}
			if err != nil {
				u.stop(err.Error())
				continue
			}
			if !result.Active {
				u.selected = ""
				message := "Choose another sound to compare."
				if result.Error != "" {
					message = result.Error
				}
				u.render(message)
			}
		}
	}
}
