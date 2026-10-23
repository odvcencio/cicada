//go:build js && wasm

package main

import "syscall/js"

// Keep sound comparison local: no score write, navigation, history entry or
// replacement of a draft's mounted input is needed to select another sound.
func (u *soundsUI) syncPatternSound(track string, explicit bool) {
	if u.props.Pattern == "" || track == "" {
		return
	}
	window := js.Global()
	base := window.Get("location").Get("href").String()
	if next, ok := patternSoundURL(base, base, u.props.Pattern, track, explicit); ok {
		history := window.Get("history")
		history.Call("replaceState", history.Get("state"), "", next)
	}
	scope := u.root.Call("closest", "#patterns")
	if !workspacePresent(scope) {
		return
	}
	links := scope.Call("querySelectorAll", "a[href]")
	for i := 0; i < links.Length(); i++ {
		link := links.Index(i)
		if next, ok := patternSoundURL(workspaceAttr(link, "href"), base, u.props.Pattern, track, false); ok {
			link.Call("setAttribute", "href", next)
		}
	}
	forms := scope.Call("querySelectorAll", "form")
	for i := 0; i < forms.Length(); i++ {
		form := forms.Index(i)
		pattern := form.Call("querySelector", "input[name=pattern]")
		if !workspacePresent(pattern) || pattern.Get("value").String() != u.props.Pattern {
			continue
		}
		field := form.Call("querySelector", "[name=track]")
		if !workspacePresent(field) {
			field = window.Get("document").Call("createElement", "input")
			field.Set("type", "hidden")
			field.Set("name", "track")
			form.Call("appendChild", field)
		}
		// Scene/placement destination pickers have their own visible choice.
		if workspaceAttr(field, "type") == "hidden" {
			field.Set("value", track)
		}
		if target := form.Call("querySelector", "input[name=__gosx_return_to]"); workspacePresent(target) {
			if next, ok := patternSoundURL(target.Get("value").String(), base, u.props.Pattern, track, true); ok {
				target.Set("value", next)
			}
		}
	}
}
