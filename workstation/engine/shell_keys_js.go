//go:build js && wasm

package main

import "syscall/js"

// Keyboard performance has one owner at a time on the /studio shell, because
// the Live keyboard and the Sampler pads both claim letter keys (a, w, s, e,
// d, f) and both stay mounted. The Sampler owns the keys only while its Browser
// section is open and holds focus. Otherwise Live owns them while the Session
// view is showing. Elsewhere on the shell nothing plays. The panel application
// at "/" mounts one of the two at a time, so there the owner is "any".
const (
	keysAny     = "any"
	keysLive    = "live"
	keysSampler = "sampler"
	keysNone    = "none"
)

func keyboardOwner() string {
	if js.Global().Get("location").Get("pathname").String() != "/studio" {
		return keysAny
	}
	document := js.Global().Get("document")
	if section := document.Call("getElementById", "browser-sampler"); !section.IsNull() && section.Get("open").Bool() {
		if active := document.Get("activeElement"); active.Type() == js.TypeObject && section.Call("contains", active).Bool() {
			return keysSampler
		}
	}
	if session := document.Call("getElementById", "view-panel-session"); !session.IsNull() && !session.Get("hidden").Bool() {
		return keysLive
	}
	return keysNone
}

// ownsKeyboard reports whether the named engine may play from the keyboard now.
func ownsKeyboard(engine string) bool {
	owner := keyboardOwner()
	return owner == keysAny || owner == engine
}

// watchKeyboardOwner calls release when the engine stops owning the keyboard,
// whatever moved the focus or the view.
func watchKeyboardOwner(listen func(js.Value, string, func(js.Value)), engine string, release func()) {
	document := js.Global().Get("document")
	for _, event := range []string{"focusin", "click", "keydown"} {
		listen(document, event, func(js.Value) {
			if !ownsKeyboard(engine) {
				release()
			}
		})
	}
}
