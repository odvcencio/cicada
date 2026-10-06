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
	"strings"
	"sync"
	"syscall/js"
	"time"
	"unicode/utf16"

	"m31labs.dev/cicada/workstation/collab"
	enginewasm "m31labs.dev/gosx/engine/wasm"
	hubclient "m31labs.dev/gosx/hub/client"
)

type sharedSnapshot struct {
	ID       string           `json:"id"`
	Revision string           `json:"revision"`
	Document *collab.Document `json:"document"`
}
type sharedRecovery struct {
	ID      string          `json:"id"`
	Replica *collab.Replica `json:"replica"`
}

func mountCollaboration(host enginewasm.Context) (enginewasm.Handle, error) {
	var props struct {
		Actor string `json:"actor"`
		Role  string `json:"role"`
		CSRF  string `json:"csrf"`
	}
	if err := host.DecodeProps(&props); err != nil {
		return nil, err
	}
	mount := host.Mount()
	document := js.Global().Get("document")
	makeNode := func(tag, label string) js.Value {
		node := document.Call("createElement", tag)
		node.Set("textContent", label)
		return node
	}
	status := makeNode("p", "Connecting to the shared score…")
	status.Call("setAttribute", "role", "status")
	presence := makeNode("p", "")
	presence.Call("setAttribute", "aria-label", "Collaborators")
	label := makeNode("label", "Shared score draft")
	label.Set("className", "field")
	editor := makeNode("textarea", "")
	editor.Set("rows", 22)
	editor.Set("spellcheck", false)
	editor.Set("readOnly", true)
	editor.Call("setAttribute", "aria-label", "Shared score draft")
	editor.Call("setAttribute", "data-collaboration-editor", "")
	editor.Get("style").Set("width", "100%")
	editor.Get("style").Set("fontFamily", "ui-monospace, monospace")
	label.Call("appendChild", editor)
	actions := makeNode("div", "")
	actions.Set("className", "actions")
	button := func(text string) js.Value {
		node := makeNode("button", text)
		node.Set("type", "button")
		actions.Call("appendChild", node)
		return node
	}
	undo, redo, save := button("Undo my edit"), button("Redo my edit"), button("Save shared score")
	undo.Set("disabled", true)
	redo.Set("disabled", true)
	save.Set("disabled", true)
	mount.Call("replaceChildren", status, presence, label, actions)
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	closed, composing := false, false
	var deferred *sharedSnapshot
	var recovery sharedRecovery
	var events []workspaceEvent
	listen := func(target js.Value, name string, fn func(js.Value)) {
		callback := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) > 0 {
				fn(args[0])
			}
			return nil
		})
		target.Call("addEventListener", name, callback)
		events = append(events, workspaceEvent{target, name, callback})
	}
	storage := js.Global().Get("sessionStorage")
	storageKey := "cicada.shared." + props.Actor
	// sessionStorage is scoped to this tab and origin. A tab has a separate
	// actor counter even when it shares the encrypted user session cookie.
	clientID := safeStorageGet(storage, storageKey+".client")
	if len(clientID) != 16 {
		var id [8]byte
		if _, err := rand.Read(id[:]); err != nil {
			cancel()
			return nil, err
		}
		clientID = hex.EncodeToString(id[:])
		safeStorageSet(storage, storageKey+".client", clientID)
	}
	actor := props.Actor + "-" + clientID
	if value := safeStorageGet(storage, storageKey); value != "" {
		_ = json.Unmarshal([]byte(value), &recovery)
		if recovery.Replica == nil || recovery.Replica.Actor != actor || recovery.Replica.Document == nil {
			recovery = sharedRecovery{}
		}
	}
	canWrite := props.Role == "owner" || props.Role == "editor"
	var connection *hubclient.Client
	stopped := false
	networkOffline := !js.Global().Get("navigator").Get("onLine").Bool()
	keep := func() {
		data, _ := json.Marshal(recovery)
		if !safeStorageSet(storage, storageKey, string(data)) {
			status.Set("textContent", "Local recovery storage is unavailable. Keep this tab open to retain offline edits.")
		}
	}
	update := func() {
		if recovery.Replica == nil {
			return
		}
		text, _ := recovery.Replica.Document.Visible()
		if editor.Get("value").String() != text {
			editor.Set("value", text)
		}
		editor.Set("readOnly", !canWrite || stopped)
		undo.Set("disabled", !canWrite || stopped || len(recovery.Replica.UndoStack) == 0)
		redo.Set("disabled", !canWrite || stopped || len(recovery.Replica.RedoStack) == 0)
		save.Set("disabled", !canWrite || stopped || networkOffline || len(recovery.Replica.Pending) > 0 || connection.State() != hubclient.StateConnected)
		if !stopped {
			message := "Synced · " + props.Role
			if networkOffline || connection.State() != hubclient.StateConnected {
				message = "Offline · edits stay in this tab"
			} else if len(recovery.Replica.Pending) > 0 {
				message = fmt.Sprintf("Syncing %d edits…", len(recovery.Replica.Pending))
			}
			status.Set("textContent", message)
		}
	}
	flush := func() {
		if recovery.Replica == nil || stopped || networkOffline {
			return
		}
		ops := recovery.Replica.Pending
		if len(ops) > 32 {
			ops = ops[:32]
		}
		// Hub text frames are bounded. A large replacement is rejected by
		// the input handler before it can become an unsendable offline edit.
		for len(ops) > 1 {
			data, _ := json.Marshal(ops)
			if len(data) < 50<<10 {
				break
			}
			ops = ops[:len(ops)/2]
		}
		_ = connection.Send("sync", struct {
			ID         string             `json:"id"`
			Operations []collab.Operation `json:"operations"`
		}{recovery.ID, ops})
	}
	applyState := func(snapshot sharedSnapshot) {
		if snapshot.Document == nil {
			return
		}
		if recovery.Replica == nil {
			recovery = sharedRecovery{snapshot.ID, &collab.Replica{Actor: actor, Document: &collab.Document{}}}
		}
		if recovery.ID != snapshot.ID {
			stopped = true
			status.Set("textContent", "A different shared draft is open. Copy your local text before reopening Studio.")
			update()
			return
		}
		old, ids := recovery.Replica.Document.Visible()
		start := runePosition(old, editor.Get("selectionStart").Int())
		end := runePosition(old, editor.Get("selectionEnd").Int())
		anchor := func(index int) collab.ID {
			if index > 0 && index <= len(ids) {
				return ids[index-1]
			}
			return collab.ID{}
		}
		startID, endID := anchor(start), anchor(end)
		if err := recovery.Replica.Receive(snapshot.Document.Operations); err != nil {
			stopped = true
			status.Set("textContent", err.Error())
			update()
			return
		}
		update()
		text, nextIDs := recovery.Replica.Document.Visible()
		position := func(id collab.ID, fallback int) int {
			if id == (collab.ID{}) {
				return 0
			}
			for i, other := range nextIDs {
				if id == other {
					return i + 1
				}
			}
			if fallback > len(nextIDs) {
				return len(nextIDs)
			}
			return fallback
		}
		editor.Call("setSelectionRange", utf16Position(text, position(startID, start)), utf16Position(text, position(endID, end)))
		keep()
		if len(recovery.Replica.Pending) > 0 {
			flush()
		}
	}
	origin := js.Global().Get("location").Get("origin").String()
	connection = hubclient.New(hubclient.Options{URL: strings.Replace(origin, "http", "ws", 1) + "/collaboration/score?client=" + clientID, OnStateChange: func(state hubclient.State) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			update()
		}
	}})
	connection.On("state", func(data json.RawMessage) {
		var snapshot sharedSnapshot
		if json.Unmarshal(data, &snapshot) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		if composing {
			deferred = &snapshot
			return
		}
		applyState(snapshot)
	})
	connection.On("error", func(data json.RawMessage) {
		var message string
		_ = json.Unmarshal(data, &message)
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			stopped = true
			update()
			status.Set("textContent", message)
		}
	})
	connection.On("presence", func(data json.RawMessage) {
		var members []struct {
			Actor  string    `json:"actor"`
			Role   string    `json:"role"`
			Cursor collab.ID `json:"cursor"`
		}
		if json.Unmarshal(data, &members) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		var names []string
		for _, member := range members {
			name := "User " + member.Actor[:min(6, len(member.Actor))] + " · " + member.Role
			if recovery.Replica != nil && member.Cursor != (collab.ID{}) {
				text, ids := recovery.Replica.Document.Visible()
				for i, id := range ids {
					if id == member.Cursor {
						name += fmt.Sprintf(" · line %d", strings.Count(string([]rune(text)[:i+1]), "\n")+1)
						break
					}
				}
			}
			names = append(names, name)
		}
		presence.Set("textContent", strings.Join(names, "   |   "))
	})
	edit := func() {
		if recovery.Replica == nil || !canWrite || stopped {
			return
		}
		// Work on a copy so an oversize paste can be rejected atomically.
		candidate := *recovery.Replica
		candidate.Document = recovery.Replica.Document.Clone()
		if err := candidate.Edit(editor.Get("value").String()); err != nil {
			update()
			status.Set("textContent", err.Error())
			return
		}
		if len(candidate.Pending) > 0 {
			data, _ := json.Marshal(candidate.Pending[len(candidate.Pending)-1])
			if len(data) > 50<<10 {
				update()
				status.Set("textContent", "This edit is too large. Paste smaller sections of the score.")
				return
			}
		}
		*recovery.Replica = candidate
		update()
		keep()
		flush()
	}
	listen(editor, "input", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		if !closed && !composing {
			edit()
		}
	})
	listen(editor, "compositionstart", func(event js.Value) { mu.Lock(); defer mu.Unlock(); composing = true })
	listen(editor, "compositionend", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		composing = false
		if closed {
			return
		}
		edit()
		if deferred != nil {
			applyState(*deferred)
			deferred = nil
		}
	})
	toggle := func(isUndo bool) {
		if recovery.Replica == nil || !canWrite || stopped {
			return
		}
		var err error
		if isUndo {
			err = recovery.Replica.Undo()
		} else {
			err = recovery.Replica.Redo()
		}
		if err != nil {
			status.Set("textContent", err.Error())
			return
		}
		update()
		keep()
		flush()
	}
	listen(undo, "click", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			toggle(true)
		}
	})
	listen(redo, "click", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			toggle(false)
		}
	})
	listen(editor, "keydown", func(event js.Value) {
		if (event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool()) && (event.Get("key").String() == "z" || event.Get("key").String() == "y") {
			event.Call("preventDefault")
			mu.Lock()
			defer mu.Unlock()
			if !closed {
				toggle(event.Get("key").String() == "z" && !event.Get("shiftKey").Bool())
			}
		}
	})
	listen(editor, "beforeinput", func(event js.Value) {
		kind := event.Get("inputType").String()
		if kind == "historyUndo" || kind == "historyRedo" {
			event.Call("preventDefault")
			mu.Lock()
			defer mu.Unlock()
			if !closed {
				toggle(kind == "historyUndo")
			}
		}
	})
	listen(editor, "select", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		if recovery.Replica == nil || closed {
			return
		}
		text, ids := recovery.Replica.Document.Visible()
		index := runePosition(text, editor.Get("selectionStart").Int())
		id := collab.ID{}
		if index > 0 && index <= len(ids) {
			id = ids[index-1]
		}
		_ = connection.Send("cursor", id)
	})
	request := func(path string, payload any, done func([]byte, error)) {
		go func() {
			data, _ := json.Marshal(payload)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, bytes.NewReader(data))
			if err != nil {
				done(nil, err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", props.CSRF)
			response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
			if err != nil {
				done(nil, err)
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			if err == nil && response.StatusCode != 200 {
				err = fmt.Errorf("%s", strings.TrimSpace(string(body)))
			}
			done(body, err)
		}()
	}
	listen(save, "click", func(event js.Value) {
		mu.Lock()
		defer mu.Unlock()
		if closed || recovery.Replica == nil || len(recovery.Replica.Pending) > 0 || !canWrite {
			return
		}
		save.Set("disabled", true)
		status.Set("textContent", "Saving shared score…")
		request("/api/collaboration/save", sharedSnapshot{ID: recovery.ID, Document: recovery.Replica.Document.Clone()}, func(data []byte, err error) {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				return
			}
			update()
			if err != nil {
				status.Set("textContent", err.Error())
			} else {
				status.Set("textContent", "Shared score saved.")
			}
		})
	})
	if props.Role == "owner" {
		inviteText := makeNode("input", "")
		inviteText.Set("readOnly", true)
		inviteText.Call("setAttribute", "aria-label", "Invitation code")
		mount.Call("appendChild", inviteText)
		for _, role := range []string{"editor", "viewer"} {
			role := role
			invite := button("Create " + role + " invitation")
			listen(invite, "click", func(event js.Value) {
				request("/api/collaboration/invite", map[string]string{"role": role}, func(data []byte, err error) {
					mu.Lock()
					defer mu.Unlock()
					if closed {
						return
					}
					if err != nil {
						status.Set("textContent", err.Error())
						return
					}
					var result struct {
						Token string `json:"token"`
					}
					_ = json.Unmarshal(data, &result)
					inviteText.Set("value", result.Token)
					status.Set("textContent", "Share this single-use invitation code. It expires in 15 minutes.")
				})
			})
		}
	} else {
		inviteText := makeNode("input", "")
		inviteText.Call("setAttribute", "aria-label", "Invitation code")
		inviteText.Set("placeholder", "Invitation code")
		mount.Call("appendChild", inviteText)
		join := button("Use invitation")
		listen(join, "click", func(event js.Value) {
			request("/api/collaboration/join", map[string]string{"token": inviteText.Get("value").String()}, func(data []byte, err error) {
				mu.Lock()
				defer mu.Unlock()
				if closed {
					return
				}
				if err != nil {
					status.Set("textContent", err.Error())
				} else {
					js.Global().Get("location").Call("reload")
				}
			})
		})
	}
	connection.Connect()
	for _, name := range []string{"offline", "online"} {
		name := name
		listen(js.Global(), name, func(event js.Value) {
			mu.Lock()
			defer mu.Unlock()
			if closed {
				return
			}
			networkOffline = name == "offline"
			update()
			if !networkOffline {
				flush()
			}
		})
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				if !closed {
					flush()
				}
				mu.Unlock()
			}
		}
	}()
	if recovery.Replica != nil {
		mu.Lock()
		update()
		mu.Unlock()
	}
	return enginewasm.HandleFunc(func() {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		closed = true
		keep()
		mu.Unlock()
		cancel()
		_ = connection.Close()
		for _, event := range events {
			event.target.Call("removeEventListener", event.name, event.callback)
			event.callback.Release()
		}
	}), nil
}

func safeStorageGet(storage js.Value, key string) (value string) {
	defer func() { _ = recover() }()
	result := storage.Call("getItem", key)
	if result.Type() == js.TypeString {
		return result.String()
	}
	return ""
}
func safeStorageSet(storage js.Value, key, value string) (ok bool) {
	defer func() { _ = recover() }()
	storage.Call("setItem", key, value)
	return true
}
func runePosition(text string, units int) int {
	used := 0
	for i, r := range []rune(text) {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if used+width > units {
			return i
		}
		used += width
	}
	return len([]rune(text))
}
func utf16Position(text string, index int) int {
	runes := []rune(text)
	if index > len(runes) {
		index = len(runes)
	}
	return len(utf16.Encode(runes[:index]))
}
