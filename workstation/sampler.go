package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

type samplerState struct {
	Revision     string          `json:"revision"`
	BankRevision string          `json:"bankRevision"`
	Bank         padsampler.Bank `json:"bank"`
	Sounds       []string        `json:"sounds"`
	CanUndo      bool            `json:"canUndo"`
}

func (s *studioApp) sampler(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score to use this panel"))
	}
	var state samplerState
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/sampler", nil, &state); err != nil {
		return failure(err)
	}
	selected, _ := strconv.Atoi(ctx.Request.URL.Query().Get("pad"))
	if selected < 1 || selected > 16 {
		selected = 1
	}
	p := state.Bank.Pads[selected-1]
	form := func(children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/sampler", CSRF: csrf, Revision: v.Revision, ReturnTo: fmt.Sprintf("/?panel=sampler&pad=%d", selected), Class: "action-form"}, append([]gosx.Node{hidden("bankRevision", state.BankRevision), hidden("source_revision", v.SourceRevision)}, children...)...)
	}
	var choices []gosx.Node
	for _, src := range state.Bank.Sources {
		choices = append(choices, gosx.El("option", gosx.Attrs(gosx.Attr("value", src.ID), gosx.Attr("selected", src.ID == p.Source)), gosx.Text(src.Name)))
	}
	sourceSelect := func(name, label string) gosx.Node {
		return gosx.El("select", gosx.Attrs(gosx.Attr("name", name), gosx.Attr("aria-label", label)), gosx.Fragment(choices...))
	}
	var pads []gosx.Node
	keys := []string{"1", "2", "3", "4", "Q", "W", "E", "R", "A", "S", "D", "F", "Z", "X", "C", "V"}
	for i, pad := range state.Bank.Pads {
		loaded := pad.Source != ""
		sub := "Empty"
		if loaded {
			sub = fmt.Sprintf("%+.0f st · %s", float64(pad.Pitch), pad.Mode)
		}
		pads = append(pads, gosx.El("div", gosx.Attrs(gosx.Attr("class", "sampler-pad"), gosx.Attr("data-pad-selected", strconv.FormatBool(i+1 == selected))),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-sampler-pad", strconv.Itoa(i+1)), gosx.Attr("aria-label", fmt.Sprintf("Play pad %d", i+1)), gosx.Attr("disabled", !loaded)), gosx.El("span", gosx.Attrs(gosx.Attr("class", "pad-number")), gosx.Text(fmt.Sprintf("%02d", i+1))), gosx.El("strong", gosx.Text(pad.Name)), gosx.El("small", gosx.Text(sub)), gosx.El("kbd", gosx.Text(keys[i]))),
			gosx.El("a", gosx.Attrs(gosx.Attr("href", fmt.Sprintf("/?panel=sampler&pad=%d", i+1)), gosx.Attr("data-sampler-edit", strconv.Itoa(i+1)), gosx.Attr("aria-label", fmt.Sprintf("Edit pad %d", i+1))), gosx.Text("Edit"))))
	}
	button := func(action, label string) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-sampler-control", action)), gosx.Text(label))
	}
	performance, _ := json.Marshal(state.Bank.Performance)
	resample := form(hidden("action", "resample"), hidden("performance", string(performance)), field("Clip name", textInput("newName", "pad-print")), field("Audio track", textInput("track", "sampler")), field("Place at", textInput("at", "@1.1.1")), field("Destination pad", wholeInput("destination", 16, 0, 16)), field("Place in Session", checkInput("place", true)), submit("", "", "Resample performance"))
	resample = gosx.El("div", gosx.Attrs(gosx.Attr("data-sampler-resample", "")), resample)
	var audio gosx.Node = gosx.Fragment()
	if len(state.Sounds) == 16 && state.Sounds[selected-1] != "" {
		audio = gosx.El("audio", gosx.Attrs(gosx.BoolAttr("controls"), gosx.Attr("preload", "none"), gosx.Attr("src", "/media/sampler/"+state.Sounds[selected-1]+".wav"), gosx.Attr("aria-label", "Selected pad preview")))
	}
	content := gosx.Fragment(gosx.El("div", gosx.Attrs(gosx.Attr("class", "sampler-pad-grid")), gosx.Fragment(pads...)),
		gosx.El("p", gosx.Attrs(gosx.Attr("data-sampler-status", ""), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite")), gosx.Text("Load a sample, then play pads with the mouse, touch, or keyboard.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "sampler-wave")), gosx.El("canvas", gosx.Attrs(gosx.Attr("data-sampler-wave", ""), gosx.Attr("width", "900"), gosx.Attr("height", "180"), gosx.Attr("aria-label", "Sample waveform and chop boundaries"), gosx.Attr("role", "img"))), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Drag either chop boundary; the source-frame fields below follow your edit."))),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions sampler-performance")), field("Record bars", wholeInput("recordBars", 4, 1, 16)), field("Quantize to sixteenths", checkInput("quantize", true)), button("record", "Record pads"), button("stop", "Stop pads"), button("play", "Play performance")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "sampler-event-strip"), gosx.Attr("data-sampler-events", ""), gosx.Attr("aria-label", "Recorded pad hits"))),
		gosx.El("details", gosx.Attrs(gosx.BoolAttr("open")), gosx.El("summary", gosx.Text("Resample to a pad and Session")), resample), audio)
	props, _ := json.Marshal(map[string]any{"csrf": csrf, "tempo": v.Project.TempoMilli, "revision": v.Revision})
	surface := meterEngine(ctx, engine.Config{Name: "CicadaSampler", Kind: engine.KindSurface, MountID: "cicada-sampler", Runtime: engine.RuntimeGoWASM, Props: props, Capabilities: []engine.Capability{engine.CapAudio, engine.CapFetch, engine.CapKeyboard, engine.CapPointer, engine.CapCanvas}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch, engine.CapAudio}}, content)
	var selectedEditor gosx.Node = gosx.El("p", gosx.Text("Import a WAV or sample a Session placement to fill the bank."))
	if len(state.Bank.Sources) > 0 {
		src, _ := state.Bank.Source(p.Source)
		frames := src.Asset.Frames
		if frames < 1 {
			frames = 1
		}
		selectedEditor = form(hidden("action", "pad"), hidden("pad", strconv.Itoa(selected)), field("Pad name", textInput("name", p.Name)), field("Sample source", sourceSelect("source", "Sample source")), field("Start frame", wholeInput("start", int(p.Start), 0, int(frames)-1)), field("End frame", wholeInput("end", int(p.End), 1, int(frames))), field("Pitch (semitones)", wholeInput("pitch", p.Pitch, -24, 24)), field("Gain (dB)", numberInput("gainDB", strconv.FormatFloat(p.GainDB, 'f', -1, 64), "-60", "12")), field("Attack (ms)", numberInput("attackMS", strconv.FormatFloat(p.AttackMS, 'f', -1, 64), "0", "500")), field("Release (ms)", numberInput("releaseMS", strconv.FormatFloat(p.ReleaseMS, 'f', -1, 64), "2", "2000")), field("Playback mode", selectInput("mode", p.Mode, []string{"oneshot", "gate"}, "Playback mode")), field("Choke group", wholeInput("choke", p.Choke, 0, 4)), field("Reverse", checkInput("reverse", p.Reverse)), field("Bypass bank effects", checkInput("bypassEffects", p.BypassEffects)), submit("", "", "Save pad"))
	}
	e := state.Bank.Effects
	fxFields := []gosx.Node{hidden("action", "effects")}
	for _, f := range []struct {
		name, label, min, max string
		value                 float64
	}{{"cutoff", "Low-pass (Hz)", "80", "20000", e.Cutoff}, {"drive", "Drive (dB)", "0", "24", e.Drive}, {"delayMS", "Delay time (ms)", "1", "1000", e.DelayMS}, {"feedback", "Delay feedback", "0", "0.7", e.Feedback}, {"delayMix", "Delay mix", "0", "1", e.DelayMix}, {"reverbMix", "Reverb mix", "0", "1", e.ReverbMix}} {
		fxFields = append(fxFields, field(f.label, numberInput(f.name, strconv.FormatFloat(f.value, 'f', -1, 64), f.min, f.max)))
	}
	fxFields = append(fxFields, submit("", "", "Apply effects"))
	fxPanel := ui.Panel(ui.PanelProps{ID: "sampler-effects", Title: "Performance effects", Description: "Effects apply to new hits. Recorded hits keep their exact sound, even after a setting changes."}, form(fxFields...), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), form(hidden("action", "effects"), submit("preset", "clean", "Clean")), form(hidden("action", "effects"), submit("preset", "dust", "Dust")), form(hidden("action", "effects"), submit("preset", "space", "Space"))))
	var load []gosx.Node
	load = append(load, gosx.El("form", gosx.Attrs(gosx.Attr("method", "post"), gosx.Attr("action", "/__sampler/import"), gosx.Attr("enctype", "multipart/form-data"), gosx.Attr("class", "action-form")), hidden("csrf_token", csrf), hidden("revision", v.Revision), hidden("bankRevision", state.BankRevision), field("WAV file", gosx.El("input", gosx.Attrs(gosx.Attr("type", "file"), gosx.Attr("name", "audio"), gosx.Attr("accept", ".wav,audio/wav"), gosx.BoolAttr("required")))), submit("", "", "Import and chop WAV")))
	if v.Project.Arrange != nil {
		var names []string
		for _, place := range v.Project.Arrange.Placements {
			names = append(names, place.ID)
		}
		load = append(load, form(hidden("action", "sample-placement"), field("Session placement", selectInput("placement", ctx.Request.URL.Query().Get("placement"), names, "Session placement")), field("Placement slices", wholeInput("count", 16, 1, 16)), submit("", "", "Sample placement")))
	}
	if len(v.Project.Assets) > 0 {
		var names []string
		for _, asset := range v.Project.Assets {
			names = append(names, asset.Name)
		}
		load = append(load, form(hidden("action", "asset"), field("Score asset", selectInput("source", "", names, "Score asset")), hidden("count", "16"), submit("", "", "Load score asset")))
	}
	if len(choices) > 0 {
		load = append(load, form(hidden("action", "chop"), field("Chop source", sourceSelect("source", "Chop source")), field("Slice count", wholeInput("count", 16, 1, 16)), submit("", "", "Split across pads")))
	}
	if state.CanUndo {
		load = append(load, form(hidden("action", "undo"), submit("", "", "Undo pad edit")))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("id", "sampler-panel")), ui.Panel(ui.PanelProps{ID: "sampler-performance", Title: "Sampler", Description: "Play, chop, and print a performance. Pad monitoring uses browser audio; resampled clips play through Session's native mixer."}, surface), gosx.El("div", gosx.Attrs(gosx.Attr("class", "sampler-editors")), ui.Panel(ui.PanelProps{ID: "sampler-pad-editor", Title: fmt.Sprintf("Pad %02d · chop and voice", selected), Description: "Source files stay intact. End is exclusive; choke 0 allows layering."}, selectedEditor), fxPanel), ui.Panel(ui.PanelProps{ID: "sampler-sources", Title: "Sample sources", Description: "Imports and equal chops fill the first pads. Undo pad edit restores the previous bank during this session."}, gosx.Fragment(load...)))
}
