//go:build js && wasm

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	u.sourceStatus("Keeping a private recovery copy… The score file is unchanged.")
}

func (u *workspaceUI) sourceStatus(text string) {
	if node := u.root.Call("querySelector", "[data-source-recovery-status]"); workspacePresent(node) {
		node.Set("textContent", text)
	}
}

// Recovery never uses the workspace mutation queue: it must not save the
// score, replace the editor's text, or delay musical edits and transport
// commands. Studio answers a checkpoint after reading only the file's
// revision, which takes no service lock.
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
			response, err := nativeRequest{Method: methodPost, Path: "/__actions/source-checkpoint", Body: data, Timeout: 5 * time.Second, Limit: 4096, Headers: map[string]string{
				"Content-Type": "application/json", "Accept": "application/json", "X-CSRF-Token": u.props.CSRF,
			}}.do(u.ctx)
			// 409: Studio refuses a checkpoint whose copy is already closed at
			// or after this edit (see below).
			refused := err == nil && response.Status == statusConflict
			if err == nil && response.Status != statusOK {
				err = fmt.Errorf("recovery returned %d", response.Status)
			}
			// Studio says whether it kept a copy: it keeps none of text the
			// file already has, and names that file's revision. Older
			// versions do not say, and keep one. An answer that cannot be
			// read is a failed attempt, like a failed request.
			var reply struct {
				Data struct {
					Kept     *bool  `json:"kept"`
					Revision string `json:"revision"`
				} `json:"data"`
			}
			if err == nil {
				if decodeErr := decodeLimited(response.Body, 4096, &reply); decodeErr != nil {
					err = fmt.Errorf("recovery answer could not be read: %w", decodeErr)
				}
			}
			kept := reply.Data.Kept == nil || *reply.Data.Kept
			if u.closed.Load() {
				return
			}
			if copy := u.sources[file]; copy == nil || copy.id != snapshot.id || copy.sequence != snapshot.sequence {
				continue
			}
			if refused {
				// A Save, a Discard or an editor back at the file's text closed
				// this copy at or after this edit; one window's checkpoint is
				// refused for nothing else. That happens to a checkpoint in
				// flight while this window saves. The copy is finished, so this
				// edit is not sent again and nothing failed; an edit typed after
				// it has a higher sequence and is kept.
				current.dirty = false
				if workspacePresent(current.control) && current.control.Get("value").String() == current.control.Get("defaultValue").String() {
					u.sourceStatus("No unsaved Code edits.")
				} else {
					u.sourceStatus("This recovery copy was closed by a Save or Discard. Your next edit keeps a new one.")
				}
				continue
			}
			if err != nil {
				consoleWarn("Code recovery checkpoint", err)
				u.sourceStatus("Recovery could not save. Keep this window open; another attempt will follow.")
				continue
			}
			current.dirty = false
			if current.clean || !kept {
				// Closed: an edit after this starts a new copy.
				delete(u.sources, file)
			}
			if workspacePresent(current.control) && current.control.Get("isConnected").Bool() {
				// History must reference an acknowledged copy. A quick file switch
				// and Back can otherwise select a checkpoint not yet on disk.
				if location, err := url.Parse(js.Global().Get("location").Get("href").String()); err == nil {
					query := location.Query()
					if current.clean || !kept {
						query.Del("codeDraft")
					} else {
						query.Set("codeDraft", current.id)
					}
					location.RawQuery = query.Encode()
					js.Global().Get("history").Call("replaceState", js.Null(), "", location.String())
					form := current.control.Call("closest", "form")
					if target := workspaceField(form, "__gosx_return_to"); workspacePresent(target) {
						target.Set("value", location.RequestURI())
					}
				}
				if current.clean {
					u.sourceStatus("No unsaved Code edits.")
				} else if !kept {
					// The file has this text: the editor takes it, at the file's
					// revision, as its own, so the Code panel is no longer held
					// and follows the file like an editor without changes.
					if form := current.control.Call("closest", "form"); reply.Data.Revision != "" && current.control.Get("value").String() == snapshot.content {
						current.control.Set("defaultValue", snapshot.content)
						if field := workspaceField(form, "fileRevision"); workspacePresent(field) {
							field.Set("value", reply.Data.Revision)
						}
						form.Call("removeAttribute", "data-code-unsaved")
					}
					u.sourceStatus("No unsaved Code edits. The score file has this text.")
				} else {
					u.sourceStatus("Recovery copy kept on this device. Use Save file to apply edits to the song.")
				}
			}
		}
	}
}
