package main

import (
	"fmt"
	"syscall/js"

	"m31labs.dev/cicada/host/browsercontrol"
	"m31labs.dev/cicada/host/demobrowser"
	"m31labs.dev/cicada/host/demopolicy"
)

func main() {
	session, err := demobrowser.NewSession()
	if err != nil {
		panic(err)
	}
	var controller *browsercontrol.Controller
	var lifecycle *browsercontrol.Lifecycle
	api := js.Global().Get("Object").New()
	bind := func(name string, call func([]js.Value) any) {
		api.Set(name, js.FuncOf(func(_ js.Value, args []js.Value) any {
			// Malformed developer-console calls cannot terminate the score tools.
			return call(args)
		}))
	}
	result := func(err error) any {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	bind("snapshot", func([]js.Value) any {
		s := session.Snapshot()
		preset := "custom"
		for _, p := range demobrowser.Presets() {
			if string(s.Source) == p.Source {
				preset = p.ID
				break
			}
		}
		return map[string]any{"source": string(s.Source), "revision": s.Revision, "canUndo": s.CanUndo, "canRedo": s.CanRedo, "preset": preset}
	})
	bind("presets", func([]js.Value) any {
		rows := []any{}
		for _, p := range demobrowser.Presets() {
			rows = append(rows, map[string]any{"id": p.ID, "name": p.Name})
		}
		return rows
	})
	bind("change", func(a []js.Value) any {
		if len(a) != 3 || a[0].Type() != js.TypeString || a[1].Type() != js.TypeString || a[2].Type() != js.TypeString {
			return "invalid score command"
		}
		revision := a[1].String()
		var err error
		switch a[0].String() {
		case "edit":
			err = session.Apply(revision, []byte(a[2].String()))
		case "undo":
			err = session.Undo(revision)
		case "redo":
			err = session.Redo(revision)
		case "reset":
			err = session.Reset(revision)
		case "preset":
			err = fmt.Errorf("unknown instrument sketch")
			for _, p := range demobrowser.Presets() {
				if p.ID == a[2].String() {
					err = session.Apply(revision, []byte(p.Source))
					break
				}
			}
		default:
			err = demopolicy.ErrDenied
		}
		return result(err)
	})
	bind("prepare", func(a []js.Value) any {
		if len(a) != 1 || a[0].Type() != js.TypeNumber {
			return "invalid sample rate"
		}
		s := session.Snapshot()
		_, _, image, err := demobrowser.Prepare(s.Source, a[0].Int())
		if err != nil {
			return err.Error()
		}
		out := js.Global().Get("Uint8Array").New(len(image))
		js.CopyBytesToJS(out, image)
		return map[string]any{"image": out, "revision": s.Revision}
	})
	bind("bind", func(a []js.Value) any {
		if len(a) != 2 || a[0].Type() != js.TypeObject || a[1].Type() != js.TypeObject {
			return "invalid audio binding"
		}
		p, cfg, _, err := demobrowser.Prepare(session.Snapshot().Source, a[1].Get("sampleRate").Int())
		if err != nil {
			return result(err)
		}
		if lifecycle != nil {
			lifecycle.Close()
		}
		controller, err = browsercontrol.New(p, cfg, demopolicy.PublicDemo(), browsercontrol.WorkletSender(a[0].Get("port")))
		if err != nil {
			return result(err)
		}
		lifecycle = browsercontrol.BindLifecycle(controller, a[1], js.Null(), func(err error) {
			a[0].Call("disconnect")
			js.Global().Call("cicadaDemoError", err.Error())
		})
		return ""
	})
	bind("play", func([]js.Value) any {
		if controller == nil {
			return browsercontrol.ErrNotPlaying.Error()
		}
		return result(controller.Play())
	})
	bind("stop", func([]js.Value) any {
		if controller == nil {
			return ""
		}
		return result(controller.Panic())
	})
	bind("close", func([]js.Value) any {
		if lifecycle != nil {
			lifecycle.Close()
			lifecycle = nil
		}
		return ""
	})
	js.Global().Set("cicadaDemo", api)
	select {}
}
