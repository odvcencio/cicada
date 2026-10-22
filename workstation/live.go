package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/project"
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
	var messages []map[string]string
	if f["type"] == "input-batch" {
		if err := json.Unmarshal([]byte(f["messages"]), &messages); err != nil || len(messages) == 0 || len(messages) > 64 {
			return action.Validation("live input batch needs 1 to 64 messages", nil, nil)
		}
	} else if f["type"] != "release" && f["type"] != "heartbeat" {
		messages = []map[string]string{f}
	}
	if f["type"] == "heartbeat" {
		request["ping"] = true
	}
	var payload []any
	for _, message := range messages {
		p, err := liveMessage(message)
		if err != nil {
			return err
		}
		payload = append(payload, p)
	}
	if len(payload) > 0 {
		request["messages"] = payload
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

func liveMessage(f map[string]string) (map[string]any, error) {
	p := map[string]any{"type": f["type"], "track": f["track"], "address": f["address"]}
	switch f["type"] {
	case "note", "note-expression", "note-group", "group-expression":
		if f["type"] == "note-group" || f["type"] == "group-expression" {
			id, err := strconv.ParseUint(f["groupId"], 10, 64)
			if err != nil || id == 0 {
				return nil, action.Validation("shared group identity must be a positive integer", nil, nil)
			}
			p["groupId"] = strconv.FormatUint(id, 10)
		}
		if f["type"] != "group-expression" && (f["noteId"] != "" || f["type"] != "note") {
			id, e := integer(f, "noteId")
			if e != nil || id < 1 || id > 65534 {
				return nil, action.Validation("note identity must be 1 to 65534", nil, nil)
			}
			p["noteId"] = id
		}
		if f["type"] == "note-group" {
			break
		}
		if f["type"] == "note-expression" || f["type"] == "group-expression" {
			for _, field := range []struct {
				key       string
				low, high float64
			}{{"pitchCents", -9600, 9600}, {"pressure", 0, 1}, {"timbre", 0, 1}} {
				n, e := finiteNumber(f, field.key)
				if e != nil || n < field.low || n > field.high {
					return nil, action.Validation("invalid note controller "+field.key, nil, nil)
				}
				p[field.key] = n
			}
			break
		}
		for _, key := range []string{"note", "velocity"} {
			n, e := integer(f, key)
			if e != nil || n < 0 || n > 127 {
				return nil, action.Validation(key+" must be 0 to 127", nil, nil)
			}
			p[key] = n
		}
		on, e := strconv.ParseBool(f["on"])
		if e != nil {
			return nil, action.Validation("note state must be true or false", nil, nil)
		}
		p["on"] = on
	case "param":
		if f["value"] == "off" {
			p["value"] = nil
		} else {
			n, e := finiteNumber(f, "value")
			if e != nil {
				return nil, action.Validation(e.Error(), nil, nil)
			}
			p["value"] = n
		}
	case "mute", "solo":
		on, e := strconv.ParseBool(f["on"])
		if e != nil {
			return nil, action.Validation("state must be true or false", nil, nil)
		}
		p["on"] = on
	case "loudness-reset":
	default:
		return nil, action.Validation("unsupported live command", nil, nil)
	}
	return p, nil
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
	var key string
	if s.notes != nil {
		saved, err := s.saveNoteCheckpoint(ctx, "ready", takes)
		if err != nil {
			return err
		}
		key = saved.ID
		s.cacheNoteDraft(saved, workspaceOwner(ctx.Request))
	} else {
		var err error
		key, err = s.storeDraft(string(data), ctx.FormData["revision"], workspaceOwner(ctx.Request), "notes")
		if err != nil {
			return err
		}
	}
	session.Current(ctx.Request).Set("note-preview", key)
	s.checkNoteDraft(ctx, key, ctx.FormData["revision"], takes)
	ctx.FormData = nil
	if action.WantsJSON(ctx.Request) {
		return ctx.Success("Take retained for review.", map[string]string{"takeId": key})
	}
	ctx.RedirectBackWithMessage("/?panel=live&notePreview="+key, "Take retained for review.")
	return nil
}

func (s *studioApp) commitNotes(ctx *action.Context) error {
	store := session.Current(ctx.Request)
	key := ctx.FormData["takeId"]
	if key == "" {
		key = store.String("note-preview")
	}
	if ctx.FormData["intent"] == "discard" {
		if d, ok := s.draft(key, workspaceOwner(ctx.Request)); ok && s.notes != nil && d.SavedNoteID != "" {
			if err := s.notes.archive(d.Owner, d.SavedNoteID, "discarded"); err != nil {
				return err
			}
		}
		s.draftMu.Lock()
		delete(s.drafts, key)
		s.draftMu.Unlock()
		store.Delete("note-preview")
		ctx.RedirectBackWithMessage("/?panel=live", "Note take discarded.")
		return nil
	}
	d, ok := s.draft(key, workspaceOwner(ctx.Request))
	if !ok || d.Kind != "notes" {
		return action.Validation("No retained note take is available.", nil, nil)
	}
	var takes []control.Recording
	if err := json.Unmarshal([]byte(d.Source), &takes); err != nil {
		return err
	}
	if ctx.FormData["intent"] == "check" {
		s.checkNoteDraft(ctx, key, ctx.FormData["revision"], takes)
		ctx.RedirectBackWithMessage("/?panel=live", "Pattern check updated. Your performance is unchanged.")
		return nil
	}
	revision := d.Revision
	if ctx.FormData["intent"] == "rebase" || ctx.FormData["intent"] == "played" {
		revision = ctx.FormData["revision"]
	}
	timing := "grid"
	if ctx.FormData["intent"] == "played" {
		timing = "played"
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/record", map[string]any{"revision": revision, "timing": timing, "recordings": takes}, nil); err != nil {
		return domainActionError(err)
	}
	message := "Recorded notes committed. Undo restores the previous patterns."
	if s.notes != nil && d.SavedNoteID != "" {
		if err := s.notes.archive(d.Owner, d.SavedNoteID, "applied"); err != nil {
			message = "Notes committed. The recovery copy could not be marked as applied; inspect your patterns before applying it again."
		}
	}
	s.draftMu.Lock()
	delete(s.drafts, key)
	s.draftMu.Unlock()
	store.Delete("note-preview")
	ctx.FormData = nil
	ctx.RedirectBackWithMessage("/?panel=live", message)
	return nil
}

// Keep a tab's review receipt explicit. Streaming input and poll responses may
// finish with an older cookie-session snapshot after a take is retained.
func notePreviewKey(r *http.Request) string {
	if id := r.URL.Query().Get("notePreview"); id != "" {
		if _, err := hex.DecodeString(id); err == nil && len(id) == 32 {
			return id
		}
		return ""
	}
	return session.Current(r).String("note-preview")
}

func (s *studioApp) live(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score before live performance"))
	}
	var pitched, drums, targets []string
	var recordingTracks []project.Track
	trackPatterns := project.RecordingPatternChoices(v.Project)
	libraryPatterns := map[string]bool{}
	for _, pattern := range v.Project.Patterns {
		libraryPatterns[pattern.ID] = true
	}
	expressionTracks := map[string]bool{}
	for _, t := range v.Project.Tracks {
		canRecord := false
		authored := false
		for _, voice := range v.Project.Instruments {
			authored = authored || voice.ID == t.Kind
		}
		if authored || t.Kind == "acid" || t.Kind == "piano" || keyboard.ID(t.Kind) != 0 {
			canRecord = true
			pitched = append(pitched, t.ID)
			expressionTracks[t.ID] = authored || t.Kind == "acid"
		}
		if t.Kind == "drums" {
			canRecord = true
			drums = append(drums, t.ID)
		}
		for _, k := range v.Project.Kits {
			if k.ID == t.Kind {
				canRecord = true
				drums = append(drums, t.ID)
			}
		}
		if canRecord {
			recordingTracks = append(recordingTracks, t)
		}
		targets = append(targets, "stop-track:"+t.ID)
		for _, slot := range t.Slots {
			if slot != nil {
				delete(libraryPatterns, *slot)
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
	patternsFor := func(tracks []string) gosx.Node {
		var choices []gosx.Node
		if len(tracks) > 0 {
			for _, id := range trackPatterns[tracks[0]] {
				label := id
				if libraryPatterns[id] {
					label += " · library"
				}
				choices = append(choices, gosx.El("option", gosx.Attrs(gosx.Attr("value", id)), gosx.Text(label)))
			}
		}
		return gosx.Fragment(choices...)
	}
	// The Go/WASM surface retains its existing selector hook; the pitched
	// targets include acid, modeled keyboards and authored mono/poly graphs.
	viewSelect := func(hook string, choices gosx.Node) gosx.Node {
		return gosx.El("select", gosx.Attrs(gosx.Attr(hook, ""), gosx.BoolAttr("data-studio-view-control")), choices)
	}
	controls := gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), field("Pitched track", viewSelect("data-live-acid", options(pitched))), field("Pitched pattern", viewSelect("data-live-pattern", patternsFor(pitched))), field("Drum track", viewSelect("data-live-drums", options(drums))), field("Drum pattern", viewSelect("data-live-drumpattern", patternsFor(drums))), button("record", "Record notes"), button("finish", "Finish note take"), button("panic", "Release notes"))
	var quantize []gosx.Node
	for _, q := range []struct{ value, label string }{{"1", "Next beat"}, {"2", "Next bar"}, {"5", "1 bar"}, {"6", "2 bars"}, {"8", "4 bars"}} {
		quantize = append(quantize, gosx.El("option", gosx.Attrs(gosx.Attr("value", q.value), gosx.Attr("selected", q.value == "2")), gosx.Text(q.label)))
	}
	var midiModes []gosx.Node
	for _, mode := range []struct{ value, label string }{{"auto", "Auto · follow controller"}, {"lower", "MPE · lower zone"}, {"upper", "MPE · upper zone"}} {
		midiModes = append(midiModes, gosx.El("option", gosx.Attrs(gosx.Attr("value", mode.value)), gosx.Text(mode.label)))
	}
	midi := gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), button("midi", "Enable MIDI"), field("Note expression", viewSelect("data-live-midimode", gosx.Fragment(midiModes...))), field("Learn target", viewSelect("data-live-target", options(targets))), field("MIDI launch timing", viewSelect("data-live-quantize", gosx.Fragment(quantize...))), button("learn", "Learn next input"), button("clear", "Clear mappings"))
	var preview gosx.Node = gosx.Fragment()
	previewKey := notePreviewKey(ctx.Request)
	if d, ok := s.draft(previewKey, workspaceOwner(ctx.Request)); ok && d.Kind == "notes" {
		preview = s.noteTakeReview(v, csrf, previewKey, d)
	}
	props, _ := json.Marshal(map[string]any{"csrf": csrf, "revision": v.Revision, "bpm": float64(v.Project.TempoMilli) / 1000, "trackPatterns": trackPatterns, "libraryPatterns": libraryPatterns, "expressionTracks": expressionTracks, "nativeMIDI": runtime.GOOS == "darwin", "preview": previewKey, "persistentNotes": s.notes != nil})
	// SSR controls are the engine's owned subtree. GoSX can preserve that
	// subtree when props are unchanged without retaining detached listeners.
	content := gosx.Fragment(controls, gosx.El("div", gosx.Attrs(gosx.Attr("class", "live-keyboard")), gosx.Fragment(keys...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "live-drums")), gosx.Fragment(pads...)), midi, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Bend, pressure and timbre follow individual held notes on acid and authored synths. Auto keeps ordinary keyboard bend ranges and follows MPE setup messages; choose a zone when your controller does not send setup. Sustain holds notes until pedal release.")), gosx.El("p", gosx.Attrs(gosx.Attr("data-live-status", ""), gosx.Attr("role", "status")), gosx.Text("Live input requires a browser with WebAssembly enabled. Launch forms remain available.")), gosx.El("ul", gosx.Attrs(gosx.Attr("data-live-mappings", ""))))
	surface := meterEngine(ctx, engine.Config{Name: "CicadaLive", Kind: engine.KindSurface, MountID: "cicada-live", Runtime: engine.RuntimeGoWASM, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapKeyboard, engine.CapPointer, engine.CapStorage}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}}, content)
	return ui.Panel(ui.PanelProps{ID: "live", Title: "Live performance", Description: "Keyboard and MIDI play acid, piano, modeled keyboards, authored instruments, and drums. Retain a take, then keep played timing or snap starts to a step pattern."}, s.liveLaunch(v, csrf), surface,
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.BoolAttr("data-live-destination-hint")), gosx.Text("Choose a pattern on this track or an unused compatible pattern from the library. Keeping a take replaces that pattern; unused patterns stay in the library until you arrange or assign them.")),
		s.patternCreationForTracks(ctx, v, csrf, "", recordingTracks, true), preview, s.noteRecovery(v, csrf, workspaceOwner(ctx.Request), previewKey, ctx.Request.URL.Query().Get("noteHistory") == "on"))
}

// Validate through the same native edit service that will commit the take.
// A failed check never replaces or discards the retained performance.
func (s *studioApp) checkNoteDraft(ctx *action.Context, key, revision string, takes []control.Recording) {
	data, _ := json.Marshal(takes)
	var recordings []edit.Recording
	err := json.Unmarshal(data, &recordings)
	for _, timing := range []string{"grid", "played"} {
		var result struct{ Valid bool }
		checkErr := err
		if checkErr == nil {
			checkErr = s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/intents", edit.Envelope{Version: edit.EnvelopeVersion, Revision: revision, DryRun: true, Intents: []edit.Intent{&edit.RecordTake{Timing: timing, Recordings: recordings}}}, &result)
		}
		message := ""
		if checkErr != nil {
			message = checkErr.Error()
		} else if !result.Valid {
			message = "The pattern check could not validate this take. Check the current patterns again."
		}
		s.draftMu.Lock()
		if d, ok := s.drafts[key]; ok && d.Owner == workspaceOwner(ctx.Request) && d.Kind == "notes" {
			if timing == "played" {
				d.PlayedCheckRevision, d.PlayedCheckError = revision, message
			} else {
				d.CheckRevision, d.CheckError = revision, message
			}
			s.drafts[key] = d
		}
		s.draftMu.Unlock()
	}
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
