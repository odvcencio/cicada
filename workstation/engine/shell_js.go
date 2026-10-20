//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	enginewasm "m31labs.dev/gosx/engine/wasm"
)

// The shell has ordinary forms as its source of truth. Commands submit those
// same forms rather than inventing a second mutation or revision path.
func mountShell(host enginewasm.Context) (enginewasm.Handle, error) {
	document := js.Global().Get("document")
	root := document.Call("getElementById", "studio-shell")
	if !workspacePresent(root) {
		return nil, fmt.Errorf("Studio shell is missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	releaseNavigation, err := bindShellNavigation(host, root)
	if err != nil {
		cancel()
		return nil, err
	}
	releasePlacementCommands := watchPlacementCommands(host, root)
	var busy atomic.Bool
	command := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 || ctx.Err() != nil {
			return nil
		}
		id := workspaceString(args[0].Get("detail"), "id")
		switch id {
		case "transport.toggle", "transport.home", "transport.record", "transport.loop", "edit.undo", "edit.redo", "edit.save", "edit.duplicate", "edit.delete":
		default:
			if !strings.HasPrefix(id, "live.scene.") {
				return nil
			}
		}
		if busy.Swap(true) {
			return nil
		}
		go func() {
			defer busy.Store(false)
			if id == "transport.loop" || id == "edit.duplicate" || id == "edit.delete" {
				submitPlacementCommand(ctx, root, id)
				return
			}
			selector := ""
			switch id {
			case "transport.toggle":
				var state struct {
					Playing bool `json:"playing"`
				}
				if !readNativeProjection(ctx, "/api/transport", &state) {
					return
				}
				action := "play"
				if state.Playing {
					action = "pause"
				}
				selector = "#transport-form button[value='" + action + "']"
			case "transport.home":
				selector = "#transport-form button[value=home]"
			case "transport.record":
				if !revealVocalWorkspace(ctx, host, root) {
					return
				}
				action := "record"
				if nativeCapture.Get().State.Active != "" {
					action = "stop"
				}
				selector = ".vocal-record [data-capture-control='" + action + "']"
			case "edit.undo":
				selector = "#undo-form button[type=submit]"
			case "edit.redo":
				selector = "#redo-form button[type=submit]"
			case "edit.save":
				// Managed navigation may resolve the form action to an absolute URL.
				// The mounted Code editor remains the save target in either form.
				selector = "#score form[data-editor-native='true'] button[type=submit]"
			default:
				scene, err := strconv.Atoi(strings.TrimPrefix(id, "live.scene."))
				if err != nil || scene < 1 || scene > 8 {
					return
				}
				selector = fmt.Sprintf("#live form[action='/__actions/launch'] thead th:nth-child(%d) button", scene+1)
			}
			if ctx.Err() != nil || !root.Get("isConnected").Bool() {
				return
			}
			button := root.Call("querySelector", selector)
			if workspacePresent(button) && !button.Get("disabled").Bool() {
				button.Call("closest", "form").Call("requestSubmit", button)
			}
		}()
		return nil
	})
	document.Call("addEventListener", "gosx:command", command)
	root.Call("setAttribute", "data-shell-actions", "ready")
	releaseAudio := audioPublisher.acquire()
	releaseCapture := capturePublisher.acquire()
	return enginewasm.HandleFunc(func() {
		cancel()
		releaseNavigation()
		releasePlacementCommands()
		releaseAudio()
		releaseCapture()
		root.Call("removeAttribute", "data-shell-actions")
		document.Call("removeEventListener", "gosx:command", command)
		command.Release()
	}), nil
}

func readNativeProjection(ctx context.Context, path string, target any) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, js.Global().Get("location").Get("origin").String()+path, nil)
	if err != nil {
		return false
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target) == nil
}
