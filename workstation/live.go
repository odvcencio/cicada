package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
	"m31labs.dev/gosx/session"
)

func (s *studioApp) liveAction(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	sequence, e := strconv.ParseUint(f["sequence"], 10, 64)
	if e != nil || sequence == 0 || len(f["lease"]) < 8 || len(f["lease"]) > 96 {
		return action.Validation("live mount lease and sequence are required", nil, nil)
	}
	identity := sha256.Sum256([]byte(workspaceOwner(ctx.Request) + ":" + f["lease"]))
	request := map[string]any{"owner": hex.EncodeToString(identity[:]), "sequence": sequence, "release": f["type"] == "release"}
	p := map[string]any{"type": f["type"], "track": f["track"], "address": f["address"]}
	switch f["type"] {
	case "release":
	case "note":
		for _, key := range []string{"note", "velocity"} {
			n, e := integer(f, key)
			if e != nil || n < 0 || n > 127 {
				return action.Validation(key+" must be 0 to 127", nil, nil)
			}
			p[key] = n
		}
		on, e := strconv.ParseBool(f["on"])
		if e != nil {
			return action.Validation("note state must be true or false", nil, nil)
		}
		p["on"] = on
	case "param":
		if f["value"] == "off" {
			p["value"] = nil
		} else {
			n, e := finiteNumber(f, "value")
			if e != nil {
				return action.Validation(e.Error(), nil, nil)
			}
			p["value"] = n
		}
	case "mute", "solo":
		on, e := strconv.ParseBool(f["on"])
		if e != nil {
			return action.Validation("state must be true or false", nil, nil)
		}
		p["on"] = on
	case "loudness-reset":
	default:
		return action.Validation("unsupported live command", nil, nil)
	}
	if f["type"] != "release" {
		request["messages"] = []any{p}
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/live", request, nil); err != nil {
		return domainActionError(err)
	}
	ctx.FormData = nil
	if !action.WantsJSON(ctx.Request) {
		ctx.RedirectBackWithMessage("/?panel=live", "")
		return nil
	}
	return ctx.Success("", nil)
}

func domainActionError(err error) error {
	var failure *backendError
	if errors.As(err, &failure) {
		return action.Error(failure.Status, failure.Message)
	}
	return action.Error(http.StatusServiceUnavailable, err.Error())
}

func (s *studioApp) previewNotes(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	var takes []control.Recording
	if err := json.Unmarshal([]byte(ctx.FormData["recordings"]), &takes); err != nil {
		return action.Validation("recordings must contain a valid note take", nil, nil)
	}
	if err := control.ValidateRecordings(takes); err != nil {
		return action.Validation(err.Error(), nil, nil)
	}
	data, _ := json.Marshal(takes)
	key, err := s.storeDraft(string(data), ctx.FormData["revision"], workspaceOwner(ctx.Request), "notes")
	if err != nil {
		return err
	}
	session.Current(ctx.Request).Set("note-preview", key)
	ctx.FormData = nil
	if action.WantsJSON(ctx.Request) {
		return ctx.Success("Take retained for review.", nil)
	}
	ctx.RedirectBackWithMessage("/?panel=live", "Take retained for review.")
	return nil
}

func (s *studioApp) commitNotes(ctx *action.Context) error {
	store := session.Current(ctx.Request)
	if ctx.FormData["intent"] == "discard" {
		store.Delete("note-preview")
		ctx.RedirectBackWithMessage("/?panel=live", "Note take discarded.")
		return nil
	}
	d, ok := s.draft(store.String("note-preview"), workspaceOwner(ctx.Request))
	if !ok || d.Kind != "notes" {
		return action.Validation("No retained note take is available.", nil, nil)
	}
	var takes []control.Recording
	if err := json.Unmarshal([]byte(d.Source), &takes); err != nil {
		return err
	}
	revision := d.Revision
	if ctx.FormData["intent"] == "rebase" {
		revision = ctx.FormData["revision"]
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/record", map[string]any{"revision": revision, "recordings": takes}, nil); err != nil {
		return domainActionError(err)
	}
	store.Delete("note-preview")
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=live", "Recorded notes committed. Undo restores the previous patterns.")
	return nil
}

func (s *studioApp) live(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score before live performance"))
	}
	var pitched, drums, targets []string
	trackPatterns := map[string][]string{}
	for _, t := range v.Project.Tracks {
		if t.Kind == "acid" || keyboard.ID(t.Kind) != 0 {
			pitched = append(pitched, t.ID)
		} else {
			for _, voice := range v.Project.Instruments {
				if voice.ID == t.Kind {
					pitched = append(pitched, t.ID)
					break
				}
			}
		}
		if t.Kind == "drums" {
			drums = append(drums, t.ID)
		}
		for _, k := range v.Project.Kits {
			if k.ID == t.Kind {
				drums = append(drums, t.ID)
			}
		}
		targets = append(targets, "stop-track:"+t.ID)
		for _, slot := range t.Slots {
			if slot != nil {
				trackPatterns[t.ID] = append(trackPatterns[t.ID], *slot)
				targets = append(targets, "slot:"+t.ID+":"+*slot)
			}
		}
	}
	for _, scene := range v.Project.Scenes {
		targets = append(targets, "scene:"+scene.ID)
	}
	targets = append(targets, "transport:play", "transport:stop")
	var params struct {
		Registry []struct {
			ID   string `json:"id"`
			Live bool   `json:"live"`
		} `json:"registry"`
		Addresses []struct {
			Address string `json:"address"`
			Param   string `json:"param"`
		} `json:"addresses"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/params", nil, &params); err == nil {
		live := map[string]bool{}
		for _, d := range params.Registry {
			live[d.ID] = d.Live
		}
		for _, a := range params.Addresses {
			if live[a.Param] {
				targets = append(targets, "param:"+a.Address)
			}
		}
	}
	var keys []gosx.Node
	for i, key := range []string{"a", "w", "s", "e", "d", "f", "t", "g", "y", "h", "u", "j", "k"} {
		keys = append(keys, gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.BoolAttr("disabled"), gosx.Attr("data-live-note", 48+i), gosx.Attr("aria-label", fmt.Sprintf("Note %d, keyboard %s", 48+i, key))), gosx.Text(strconv.Itoa(48+i)+" · "+key)))
	}
	var pads []gosx.Node
	for _, n := range []int{36, 38, 42, 46, 39, 37, 41, 45, 48, 56, 49} {
		pads = append(pads, gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.BoolAttr("disabled"), gosx.Attr("data-live-drum", n), gosx.Attr("aria-label", fmt.Sprintf("General MIDI drum %d", n))), gosx.Text(strconv.Itoa(n))))
	}
	button := func(id, label string) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("disabled", id == "record" || id == "finish"), gosx.Attr("data-live-control", id)), gosx.Text(label))
	}
	patternsFor := func(tracks []string) []string {
		if len(tracks) > 0 {
			return trackPatterns[tracks[0]]
		}
		return nil
	}
	// The Go/WASM surface retains its existing selector hook; the pitched
	// targets include acid, modeled keyboards and authored mono/poly graphs.
	controls := gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), field("Pitched track", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-acid", "")), options(pitched))), field("Pitched pattern", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-pattern", "")), options(patternsFor(pitched)))), field("Drum track", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-drums", "")), options(drums))), field("Drum pattern", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-drumpattern", "")), options(patternsFor(drums)))), button("record", "Record notes"), button("finish", "Finish note take"), button("panic", "Release notes"))
	var quantize []gosx.Node
	for _, q := range []struct{ value, label string }{{"1", "Next beat"}, {"2", "Next bar"}, {"5", "1 bar"}, {"6", "2 bars"}, {"8", "4 bars"}} {
		quantize = append(quantize, gosx.El("option", gosx.Attrs(gosx.Attr("value", q.value), gosx.Attr("selected", q.value == "2")), gosx.Text(q.label)))
	}
	midi := gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), button("midi", "Enable MIDI"), field("Learn target", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-target", "")), options(targets))), field("MIDI launch timing", gosx.El("select", gosx.Attrs(gosx.Attr("data-live-quantize", "")), gosx.Fragment(quantize...))), button("learn", "Learn next input"), button("clear", "Clear mappings"))
	var preview gosx.Node = gosx.Fragment()
	if d, ok := s.draft(session.Current(ctx.Request).String("note-preview"), workspaceOwner(ctx.Request)); ok && d.Kind == "notes" {
		apply := submit("intent", "commit", "Commit notes to patterns")
		if d.Revision != v.Revision {
			apply = gosx.Fragment(gosx.El("p", gosx.Text("The score changed since this take. Review the notes and current target patterns before applying.")), submit("intent", "rebase", "Apply notes to current score"))
		}
		preview = gosx.El("section", gosx.Attrs(gosx.Attr("class", "note-preview")), gosx.El("h3", gosx.Text("Retained note take")), gosx.El("pre", gosx.Text(d.Source)), s.form(v, csrf, "live", "note-commit", apply, submit("intent", "discard", "Discard note take")))
	}
	props, _ := json.Marshal(map[string]any{"csrf": csrf, "revision": v.Revision, "bpm": float64(v.Project.TempoMilli) / 1000, "trackPatterns": trackPatterns, "preview": session.Current(ctx.Request).String("note-preview")})
	// SSR controls are the engine's owned subtree. GoSX can preserve that
	// subtree when props are unchanged without retaining detached listeners.
	content := gosx.Fragment(controls, gosx.El("div", gosx.Attrs(gosx.Attr("class", "live-keyboard")), gosx.Fragment(keys...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "live-drums")), gosx.Fragment(pads...)), midi, gosx.El("p", gosx.Attrs(gosx.Attr("data-live-status", ""), gosx.Attr("role", "status")), gosx.Text("Live input requires a browser with WebAssembly enabled. Launch forms remain available.")), gosx.El("ul", gosx.Attrs(gosx.Attr("data-live-mappings", ""))))
	surface := ctx.Engine(engine.Config{Name: "CicadaLive", Kind: engine.KindSurface, MountID: "cicada-live", Runtime: engine.RuntimeGoWASM, WASMPath: ui.MeterEnginePath, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapKeyboard, engine.CapPointer, engine.CapStorage}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, content)
	return ui.Panel(ui.PanelProps{ID: "live", Title: "Live performance", Description: "Keyboard and Web MIDI play acid, modeled keyboards, authored instruments, and drums through Tymbal. Note patterns retain single-note takes; overlapping polyphonic takes need separate patterns."}, s.liveLaunch(v, csrf), surface, preview)
}

type liveLaunchCommand struct {
	Action   string `json:"action"`
	Scene    string `json:"scene,omitempty"`
	Track    string `json:"track,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
	Revision string `json:"revision,omitempty"`
	Quantize int    `json:"quantize,omitempty"`
}

func (s *studioApp) launchAction(ctx *action.Context) error {
	return s.mutation("/api/transport", func(f map[string]string) (any, error) {
		var command liveLaunchCommand
		if err := json.Unmarshal([]byte(f["command"]), &command); err != nil {
			return nil, fmt.Errorf("choose a valid scene or slot")
		}
		if command.Action != "launch" && command.Action != "slot" && command.Action != "trackStop" {
			return nil, fmt.Errorf("unsupported launch command")
		}
		q, err := integer(f, "quantize")
		if err != nil {
			return nil, err
		}
		command.Quantize, command.Revision = q, f["revision"]
		return command, nil
	})(ctx)
}

func (s *studioApp) liveLaunch(v workspace, csrf string) gosx.Node {
	var heads, rows []gosx.Node
	button := func(label string, command liveLaunchCommand) gosx.Node {
		data, _ := json.Marshal(command)
		return submit("command", string(data), label)
	}
	timing := func() gosx.Node {
		var opts []gosx.Node
		for _, q := range []struct{ value, label string }{{"2", "Next bar"}, {"1", "Next beat"}, {"5", "1 bar"}, {"6", "2 bars"}, {"8", "4 bars"}} {
			opts = append(opts, gosx.El("option", gosx.Attrs(gosx.Attr("value", q.value)), gosx.Text(q.label)))
		}
		return field("Launch timing", gosx.El("select", gosx.Attrs(gosx.Attr("name", "quantize")), gosx.Fragment(opts...)))
	}
	heads = append(heads, gosx.El("th", gosx.Text("Track")))
	for _, scene := range v.Project.Scenes {
		heads = append(heads, gosx.El("th", button(scene.ID, liveLaunchCommand{Action: "launch", Scene: scene.ID})))
	}
	heads = append(heads, gosx.El("th", gosx.Text("Stop track")))
	for _, track := range v.Project.Tracks {
		cells := []gosx.Node{gosx.El("th", gosx.Text(track.ID))}
		for _, scene := range v.Project.Scenes {
			pattern := scene.Bindings[track.ID]
			if pattern == "" || pattern == "keep" {
				cells = append(cells, gosx.El("td", gosx.Text("Keep")))
				continue
			}
			action, label := "slot", pattern
			if pattern == "off" {
				action, label = "trackStop", "Stop"
			}
			cells = append(cells, gosx.El("td", gosx.Attrs(gosx.Attr("data-live-slot", pattern), gosx.Attr("data-live-track", track.ID)), button(label, liveLaunchCommand{Action: action, Track: track.ID, Pattern: pattern})))
		}
		cells = append(cells, gosx.El("td", button("Stop "+track.ID, liveLaunchCommand{Action: "trackStop", Track: track.ID})))
		rows = append(rows, gosx.El("tr", gosx.Fragment(cells...)))
	}
	return gosx.El("details", gosx.Attrs(gosx.BoolAttr("open")), gosx.El("summary", gosx.Text("Live launch matrix")), gosx.El("p", gosx.Attrs(gosx.Attr("data-live-launch-status", ""), gosx.Attr("aria-live", "off")), gosx.Text("Launch scenes or individual slots while playing.")), s.form(v, csrf, "live", "launch", timing(), gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Live launch matrix")), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...))))))
}

func options(values []string) gosx.Node {
	var nodes []gosx.Node
	for _, v := range values {
		nodes = append(nodes, gosx.El("option", gosx.Attrs(gosx.Attr("value", v)), gosx.Text(v)))
	}
	return gosx.Fragment(nodes...)
}
