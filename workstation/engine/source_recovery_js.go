//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"syscall/js"
	"time"
)

type sourceRecovery struct {
	id, base, file, content, parent, parentHash string
	sequence                                    uint64
	clean, dirty                                bool
	control                                     js.Value
	lastAttempt                                 time.Time
}

func bindSourceRecovery(ctx context.Context, root js.Value, csrf string) func() {
	u := &workspaceUI{root: root, ctx: ctx, props: workspaceProps{CSRF: csrf}, editors: map[string]workspaceEditorBaseline{}}
	u.seedEditorBaseline()
	input := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && !u.closed.Load() {
			u.sourceInput(args[0])
		}
		return nil
	})
	document := js.Global().Get("document")
	document.Call("addEventListener", "input", input, true)
	go u.checkpointSources()
	return func() {
		u.closed.Store(true)
		document.Call("removeEventListener", "input", input, true)
		input.Release()
	}
}

func (u *workspaceUI) sourceInput(event js.Value) {
	control := event.Get("target")
	if !u.owned(control) || !control.Call("matches", "#score textarea").Bool() || !workspacePresent(u.root.Call("querySelector", "[data-source-recovery-status]")) {
		return
	}
	u.captureSource(control)
}

func (u *workspaceUI) captureSource(control js.Value) {
	form := control.Call("closest", "form")
	file, base := workspaceFieldValue(form, "file"), workspaceFieldValue(form, "fileRevision")
	if base == "" {
		base = workspaceFieldValue(form, "revision")
	}
	clean := control.Get("value").String() == control.Get("defaultValue").String() && !form.Call("hasAttribute", "data-code-unsaved").Bool()
	if u.sources == nil {
		u.sources = make(map[string]*sourceRecovery)
	}
	copy := u.sources[file]
	if copy == nil || copy.base != base || copy.clean {
		if clean {
			return
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			u.sourceStatus("Recovery could not start. Keep this window open.")
			return
		}
		copy = &sourceRecovery{id: hex.EncodeToString(id[:]), file: file, base: base, parent: workspaceFieldValue(form, "recoveryId"), parentHash: workspaceFieldValue(form, "recoveryHash")}
		u.sources[file] = copy
	}
	copy.sequence++
	copy.content, copy.clean, copy.dirty, copy.control = control.Get("value").String(), clean, true, control
	for name, value := range map[string]string{"recoveryId": copy.id, "recoverySequence": strconv.FormatUint(copy.sequence, 10), "recoveryParentId": copy.parent, "recoveryParentHash": copy.parentHash} {
		if node := workspaceField(form, name); workspacePresent(node) {
			node.Set("value", value)
		}
	}
	if location, err := url.Parse(js.Global().Get("location").Get("href").String()); err == nil {
		query := location.Query()
		query.Set("codeDraft", copy.id)
		location.RawQuery = query.Encode()
		js.Global().Get("history").Call("replaceState", js.Null(), "", location.String())
		if target := workspaceField(form, "__gosx_return_to"); workspacePresent(target) {
			target.Set("value", location.RequestURI())
		}
	}
	u.sourceStatus("Keeping a private recovery copy… The score file is unchanged.")
}

func (u *workspaceUI) sourceStatus(text string) {
	if node := u.root.Call("querySelector", "[data-source-recovery-status]"); workspacePresent(node) {
		node.Set("textContent", text)
	}
}

// Recovery never uses the workspace mutation queue: it must not save the
// score, replace the editor, or delay musical edits and transport commands.
func (u *workspaceUI) checkpointSources() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	if control := u.root.Call("querySelector", "#score textarea"); workspacePresent(control) {
		value, baseline := control.Get("value").String(), control.Get("defaultValue").String()
		if value != baseline && value != workspaceEditorNormalized(baseline) {
			u.captureSource(control)
		}
	}
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		}
		for file, current := range u.sources {
			if active := u.root.Call("querySelector", "#score textarea"); workspacePresent(active) && workspaceFieldValue(active.Call("closest", "form"), "file") == file {
				current.control = active
			}
			if workspacePresent(current.control) && current.control.Get("isConnected").Bool() {
				form := current.control.Call("closest", "form")
				matchesFile := workspaceFieldValue(form, "file") == file
				if base := workspaceFieldValue(form, "fileRevision"); matchesFile && base != "" && base != current.base {
					delete(u.sources, file)
					u.sourceStatus("No unsaved Code edits.")
					continue
				}
				if matchesFile && !current.clean && current.control.Get("value").String() == current.control.Get("defaultValue").String() && !form.Call("hasAttribute", "data-code-unsaved").Bool() {
					current.clean, current.dirty = true, true
					current.sequence++
				}
			}
			if !current.dirty || time.Since(current.lastAttempt) < time.Second {
				continue
			}
			current.lastAttempt = time.Now()
			snapshot := *current
			fields := map[string]string{"file": snapshot.file, "fileRevision": snapshot.base, "content": snapshot.content, "recoveryId": snapshot.id, "recoverySequence": strconv.FormatUint(snapshot.sequence, 10), "recoveryParentId": snapshot.parent, "recoveryParentHash": snapshot.parentHash}
			if snapshot.clean {
				fields["intent"] = "clear"
			}
			data, _ := json.Marshal(fields)
			origin := js.Global().Get("location").Get("origin").String()
			ctx, cancel := context.WithTimeout(u.ctx, 5*time.Second)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/__actions/source-checkpoint", bytes.NewReader(data))
			if err != nil {
				cancel()
				continue
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json")
			request.Header.Set("X-CSRF-Token", u.props.CSRF)
			response, err := http.DefaultClient.Do(request)
			if response != nil {
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("recovery returned %d", response.StatusCode)
				}
			}
			cancel()
			if u.closed.Load() {
				return
			}
			if copy := u.sources[file]; copy == nil || copy.id != snapshot.id || copy.sequence != snapshot.sequence {
				continue
			}
			if err != nil {
				u.sourceStatus("Recovery could not save. Keep this window open; another attempt will follow.")
				continue
			}
			current.dirty = false
			if current.clean {
				delete(u.sources, file)
			}
			if workspacePresent(current.control) && current.control.Get("isConnected").Bool() {
				if current.clean {
					u.sourceStatus("No unsaved Code edits.")
				} else {
					u.sourceStatus("Recovery copy kept on this device. Use Save file to apply edits to the song.")
				}
			}
		}
	}
}
