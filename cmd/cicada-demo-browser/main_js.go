package main

import (
	"fmt"
	"math"
	"syscall/js"

	"m31labs.dev/cicada/host/browsercontrol"
	"m31labs.dev/cicada/host/demobrowser"
	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/patternmachine"
)

func main() {
	session, err := demobrowser.NewSession()
	if err != nil {
		panic(err)
	}
	var controller *browsercontrol.Controller
	var lifecycle *browsercontrol.Lifecycle
	var overviewRevision string
	var overview map[string]any
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
		if overviewRevision != s.Revision {
			overview = map[string]any{"title": "", "tempo": 0, "tracks": []any{}, "scenes": []any{}}
			if p, err := demobrowser.Parse(s.Source); err == nil {
				tracks := make([]any, 0, len(p.Tracks))
				for _, track := range p.Tracks {
					drums := track.Kind == "drums"
					for _, kit := range p.Kits {
						if kit.ID == track.Kind {
							drums = true
						}
					}
					tracks = append(tracks, map[string]any{"name": track.ID, "instrument": track.Kind, "drums": drums})
				}
				scenes := make([]any, 0, len(p.Scenes))
				for _, scene := range p.Scenes {
					scenes = append(scenes, scene.ID)
				}
				overview = map[string]any{"title": p.Title, "tempo": float64(p.TempoMilli) / 1000, "tracks": tracks, "scenes": scenes, "arranged": p.Arrange != nil}
			}
			overviewRevision = s.Revision
		}
		preset := "custom"
		for _, p := range demobrowser.Presets() {
			if string(s.Source) == p.Source {
				preset = p.ID
				break
			}
		}
		return map[string]any{"source": string(s.Source), "revision": s.Revision, "canUndo": s.CanUndo, "canRedo": s.CanRedo, "preset": preset, "overview": overview}
	})
	bind("generate", func(a []js.Value) any {
		if len(a) != 7 || a[0].Type() != js.TypeString || a[1].Type() != js.TypeString || a[2].Type() != js.TypeString {
			return "invalid pattern request"
		}
		values := make([]int, 4)
		for i := range values {
			if a[i+3].Type() != js.TypeNumber {
				return "invalid pattern control"
			}
			n := a[i+3].Float()
			if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < 0 || n > 1000000000 {
				return "invalid pattern control"
			}
			values[i] = int(n)
		}
		s := session.Snapshot()
		if s.Revision != a[0].String() {
			return "score changed; generate a new idea"
		}
		recipe := patternmachine.Recipe{Version: 1, Seed: uint32(values[0]), Bars: values[1], Density: values[2], Motion: values[3]}
		idea, err := demobrowser.GenerateMachine(s.Source, a[1].String(), a[2].String(), recipe)
		if err != nil {
			return err.Error()
		}
		notes := make([]any, 0, len(idea.Pattern.Notes))
		for _, note := range idea.Pattern.Notes {
			name := ""
			if note.Velocity > 0 {
				name = patternmachine.Note(note.Pitch)
			}
			notes = append(notes, map[string]any{"name": name, "velocity": note.Velocity})
		}
		lanes := make([]any, 0, len(idea.Lanes))
		for _, lane := range idea.Lanes {
			values := make([]any, 0, len(idea.Pattern.Lanes[lane]))
			for _, value := range idea.Pattern.Lanes[lane] {
				values = append(values, value)
			}
			lanes = append(lanes, map[string]any{"name": lane, "steps": values})
		}
		return map[string]any{"source": string(idea.Source), "name": idea.Name, "track": idea.Track, "scene": idea.Scene, "kind": idea.Kind, "notes": notes, "lanes": lanes}
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
