package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/editor"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) panel(ctx *server.Context, v workspace, csrf, panel string) gosx.Node {
	switch panel {
	case "live":
		return s.live(ctx, v, csrf)
	case "code":
		return s.code(ctx, v, csrf)
	case "session":
		return gosx.Fragment(s.session(v, csrf), s.automation(ctx, v, csrf))
	case "patterns":
		return s.patterns(ctx, v, csrf)
	case "generator":
		return s.generator(ctx, v, csrf)
	case "voices", "instruments":
		return s.instruments(ctx, v, csrf)
	case "mixer":
		return s.mixer(ctx, v, csrf)
	case "audio":
		return s.audio(ctx, v, csrf)
	case "takes":
		return s.takes(ctx, v, csrf)
	case "history":
		return s.history(ctx, v, csrf)
	case "export":
		return s.export(ctx, v, csrf)
	default:
		ctx.SetStatus(http.StatusNotFound)
		return failure(fmt.Errorf("unknown workspace panel"))
	}
}

func (s *studioApp) code(ctx *server.Context, v workspace, csrf string) gosx.Node {
	// Textareas normalize CRLF to LF. Decorate the exact browser text while
	// retaining the original disk revision for the eventual write.
	v.Source = strings.ReplaceAll(v.Source, "\r\n", "\n")
	buttons := []editor.FormButton{{Label: "Save file", Class: "primary"}, {Name: "intent", Value: "check", Label: "Check file"}}
	fileRevision := v.FileRevision
	if fileRevision == "" {
		fileRevision = v.Revision
	}
	returnTo := "/?panel=code&file=" + url.QueryEscape(v.File)
	var fileLinks []gosx.Node
	var diagnostics []gosx.Node
	for _, file := range v.Files {
		fileLinks = append(fileLinks, gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=code&file="+url.QueryEscape(file.Name)), gosx.Attr("aria-current", map[bool]string{true: "page", false: "false"}[file.Name == v.File])), gosx.Text(file.Name)))
		for _, d := range file.Diagnostics {
			diagnostics = append(diagnostics, gosx.El("li", gosx.Text(fmt.Sprintf("%s:%d:%d %s: %s", file.Name, d.Position.Line, d.Position.Column, d.Code, d.Message))))
		}
	}
	var conflict gosx.Node = gosx.Fragment()
	if v.HasDraft && fileRevision != v.DiskRevision {
		buttons = []editor.FormButton{{Name: "intent", Value: "merge", Label: "Save combined draft", Class: "primary"}}
		conflict = gosx.El("details", gosx.Attrs(gosx.BoolAttr("open")), gosx.El("summary", gosx.Text("Current file: combine its changes with your draft below")), gosx.El("pre", gosx.Attrs(gosx.Attr("class", "history-diff")), gosx.Text(v.DiskSource)))
	}
	if v.HasDraft {
		buttons = append(buttons, editor.FormButton{Name: "intent", Value: "discard", Label: "Discard draft"})
	}
	var highlights []editor.HighlightSpan
	if spans, err := language.Highlight([]byte(v.Source)); err == nil {
		offsets := utf16Offsets(v.Source)
		for _, span := range spans {
			highlights = append(highlights, editor.HighlightSpan{StartByte: span.Start, EndByte: span.End, StartUTF16: offsets[span.Start], EndUTF16: offsets[span.End], Capture: span.Capture})
		}
	}
	id := "source-editor"
	if len(v.Files) > 1 {
		hash := sha256.Sum256([]byte(v.File))
		id += fmt.Sprintf("-%x", hash[:4])
	}
	e := editor.New(id, editor.Options{
		Surface: editor.SurfaceCode, Content: v.Source, Title: v.Filename, Label: "Cicada score", Language: editor.Lang("cicada"), Theme: editor.ThemeDark,
		FormAction: "/__actions/source", CSRFToken: csrf,
		ExtraFields: map[string]string{"revision": v.Revision, "fileRevision": fileRevision, "file": v.File, "diskRevision": v.DiskRevision, action.ReturnTargetField: returnTo},
		Code:        &editor.CodeOptions{Language: "cicada", TabWidth: 2, InsertSpaces: true, Gutter: true, Highlights: highlights},
		Buttons:     buttons,
		Panels:      []editor.Panel{},
	})
	return ui.Panel(ui.PanelProps{ID: "score", Title: "Score", Description: "Changes validate before saving and land at the next bar."}, gosx.El("nav", gosx.Attrs(gosx.Attr("class", "toolbar"), gosx.Attr("aria-label", "Project files")), gosx.Fragment(fileLinks...)), gosx.El("ul", gosx.Fragment(diagnostics...)), conflict, e.Render())
}

// Map parser byte positions to the editor's UTF-16 positions in one pass.
func utf16Offsets(text string) []int {
	positions := make([]int, len(text)+1)
	units := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		for b := 0; b < size; b++ {
			positions[i+b] = units
		}
		units++
		if r > 0xffff {
			units++
		}
		i += size
		positions[i] = units
	}
	return positions
}

func (s *studioApp) session(v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score to open the session"))
	}
	p := v.Project
	var heads []gosx.Node
	heads = append(heads, gosx.El("th", gosx.Text("Track")))
	for _, scene := range p.Scenes {
		heads = append(heads, gosx.El("th", s.form(v, csrf, "session", "transport", hidden("scene", scene.ID), hidden("quantize", "2"), submit("action", "launch", scene.ID))))
	}
	var rows []gosx.Node
	for _, track := range p.Tracks {
		cells := []gosx.Node{gosx.El("th", gosx.Text(track.ID))}
		for _, scene := range p.Scenes {
			binding := scene.Bindings[track.ID]
			if binding == "" || binding == "keep" {
				cells = append(cells, gosx.El("td", gosx.Text("Keep")))
				continue
			}
			command, label := "slot", binding
			if binding == "off" {
				command, label = "trackStop", "Stop"
			}
			cells = append(cells, gosx.El("td", s.form(v, csrf, "session", "transport", hidden("track", track.ID), hidden("pattern", binding), hidden("quantize", "2"), submit("action", command, label))))
		}
		rows = append(rows, gosx.El("tr", gosx.Fragment(cells...)))
	}
	return gosx.Fragment(s.arrangement(v, csrf), ui.Panel(ui.PanelProps{ID: "session", Title: "Scene launch matrix", Description: "Scene launches are quantized to the next bar."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Scene launch matrix")), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...))))))
}

var drumOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}

func field(label string, input gosx.Node) gosx.Node {
	return gosx.El("label", gosx.Attrs(gosx.Attr("class", "field")), gosx.El("span", gosx.Text(label)), input)
}
func textInput(name, value string) gosx.Node {
	return gosx.El("input", gosx.Attrs(gosx.Attr("name", name), gosx.Attr("value", value)))
}
func numberInput(name, value, min, max string) gosx.Node {
	return gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", name), gosx.Attr("value", value), gosx.Attr("min", min), gosx.Attr("max", max), gosx.Attr("step", "any")))
}
func selectInput(name, value string, choices []string) gosx.Node {
	var options []gosx.Node
	for _, choice := range choices {
		attrs := gosx.Attrs(gosx.Attr("value", choice))
		if choice == value {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		options = append(options, gosx.El("option", attrs, gosx.Text(choice)))
	}
	return gosx.El("select", gosx.Attrs(gosx.Attr("name", name)), gosx.Fragment(options...))
}

func (s *studioApp) mixer(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var mixer mixerState
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/mixer", nil, &mixer); err != nil {
		return failure(err)
	}
	v.Revision = mixer.Revision
	v.Edition = mixer.Edition
	var strips []gosx.Node
	for _, strip := range append(append(mixer.Tracks, mixer.Buses...), mixer.Master) {
		var fields []gosx.Node
		keys := make([]string, 0, len(strip.Fields))
		for key := range strip.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fields = append(fields, s.mixerField(v, csrf, key, strip.Fields[key]))
		}
		for _, send := range strip.Sends {
			fields = append(fields, s.mixerField(v, csrf, "send to "+send.To, send.mixerField))
		}
		strips = append(strips, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("h3", gosx.Text(strip.Name)), gosx.Fragment(fields...)))
	}
	for _, ret := range append(mixer.Returns, mixer.Effects...) {
		var fields []gosx.Node
		for _, f := range ret.Fields {
			fields = append(fields, s.mixerField(v, csrf, f.Path, f))
		}
		strips = append(strips, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("h3", gosx.Text(ret.Name)), gosx.Fragment(fields...)))
	}
	return ui.Panel(ui.PanelProps{ID: "mixer", Title: "Mixer", Description: "Track, bus, return, effect, and master parameters are saved to the score."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "strips")), gosx.Fragment(strips...)))
}

func (s *studioApp) mixerField(v workspace, csrf, label string, f mixerField) gosx.Node {
	if !f.Supported {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(label+": "+f.Reason))
	}
	value := fmt.Sprint(f.Value)
	valueType := "text"
	input := textInput("value", value)
	descriptor := f.Descriptor
	if descriptor.Unit != "" {
		label += " (" + descriptor.Unit + ")"
	}
	switch f.Value.(type) {
	case float64:
		valueType = "number"
		step := "any"
		if descriptor.DisplayStep > 0 {
			step = strconv.FormatFloat(descriptor.DisplayStep, 'f', -1, 64)
		}
		input = gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("step", step), gosx.Attr("min", descriptor.Min), gosx.Attr("max", descriptor.Max), gosx.Attr("name", "value"), gosx.Attr("value", value)))
	case bool:
		valueType = "bool"
		input = selectInput("value", value, []string{"false", "true"})
	}
	if descriptor.Curve == "toggle" {
		// Default registry values are numeric, while explicit source values are
		// booleans. Both use the same domain boolean action.
		valueType = "bool"
		value = strconv.FormatBool(f.Value == true || f.Value == float64(1))
		input = gosx.El("select", gosx.Attrs(gosx.Attr("name", "value")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "false"), gosx.Attr("selected", value == "false")), gosx.Text("Off")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "true"), gosx.Attr("selected", value == "true")), gosx.Text("On")))
	}
	if descriptor.Off {
		valueType = "text"
		input = textInput("value", value)
		label += " · off or " + fmt.Sprintf("%g to %g", descriptor.Min, descriptor.Max)
	}
	if descriptor.Curve == "enum" && len(descriptor.Values) > 0 {
		valueType = "text"
		input = selectInput("value", value, descriptor.Values)
	}
	if len(f.Choices) > 0 {
		input = selectInput("value", value, f.Choices)
	}
	fields := []gosx.Node{hidden("path", f.Path), hidden("valueType", valueType), field(label, input), submit("", "", "Set")}
	if v.Edition == 1 {
		fields = append(fields, gosx.El("label", gosx.Attrs(gosx.Attr("class", "upgrade-confirm")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "confirmUpgrade"), gosx.BoolAttr("required"))), gosx.Text("Upgrade score to edition 2 for this change")))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-control")), s.form(v, csrf, "mixer", "mixer", fields...))
}

func (s *studioApp) audio(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var state audioState
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/audio/config", nil, &state); err != nil {
		return failure(err)
	}
	inputs := []gosx.Node{gosx.El("option", gosx.Attrs(gosx.Attr("value", "")), gosx.Text("System default"))}
	outputs := append([]gosx.Node(nil), inputs...)
	for _, device := range state.Inventory.Devices {
		option := func(selected bool) gosx.Node {
			a := gosx.Attrs(gosx.Attr("value", device.ID))
			if selected {
				a = append(a, gosx.BoolAttr("selected"))
			}
			return gosx.El("option", a, gosx.Text(device.Name))
		}
		if device.Inputs > 0 {
			inputs = append(inputs, option(device.ID == state.Options.InputDevice))
		}
		if device.Outputs > 0 {
			outputs = append(outputs, option(device.ID == state.Options.OutputDevice))
		}
	}
	checkbox := func(name, label string, checked bool) gosx.Node {
		a := gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", name))
		if checked {
			a = append(a, gosx.BoolAttr("checked"))
		}
		return field(label, gosx.El("input", a))
	}
	button := gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("disabled", state.Playing)), gosx.Text("Apply audio settings"))
	form := s.form(v, csrf, "audio", "audio", field("Input", gosx.El("select", gosx.Attrs(gosx.Attr("name", "inputDevice")), gosx.Fragment(inputs...))), field("Output", gosx.El("select", gosx.Attrs(gosx.Attr("name", "outputDevice")), gosx.Fragment(outputs...))), checkbox("inputEnabled", "Enable input", state.Options.InputEnabled), checkbox("monitorMuted", "Mute monitoring", state.Options.MonitorMuted), field("Monitor gain", numberInput("monitorGain", strconv.FormatFloat(state.Options.MonitorGain, 'f', -1, 64), "0", "2")), field("Monitor mode", selectInput("monitorMode", state.Options.MonitorMode, []string{"stereo", "mono1", "mono2"})), button)
	return ui.Panel(ui.PanelProps{ID: "audio", Title: "Audio devices", Description: state.Status}, gosx.El("p", gosx.Text(state.Inventory.BackendName+" · "+state.Inventory.Message)), form)
}

func (s *studioApp) history(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var history struct {
		Edits []struct {
			ID    uint64 `json:"id"`
			Label string `json:"label"`
			Diff  string `json:"diff"`
		} `json:"edits"`
		Events []struct {
			Bar    int64  `json:"bar"`
			Step   int64  `json:"step"`
			Detail string `json:"detail"`
		} `json:"events"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/history", nil, &history); err != nil {
		return failure(err)
	}
	var rows []gosx.Node
	for index := len(history.Edits) - 1; index >= 0; index-- {
		edit := history.Edits[index]
		rows = append(rows, gosx.El("details", gosx.El("summary", gosx.Text(edit.Label)), gosx.El("pre", gosx.Attrs(gosx.Attr("class", "history-diff")), gosx.Text(edit.Diff)), s.form(v, csrf, "history", "revert", hidden("id", strconv.FormatUint(edit.ID, 10)), submit("", "", "Restore this revision"))))
	}
	for _, event := range history.Events {
		rows = append(rows, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(fmt.Sprintf("Bar %d / step %d · %s", event.Bar, event.Step, event.Detail))))
	}
	return ui.Panel(ui.PanelProps{ID: "history", Title: "History", Description: "Source changes and transport events."}, gosx.Fragment(rows...))
}

func (s *studioApp) voices(v workspace) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score to inspect instruments"))
	}
	var cards []gosx.Node
	for _, track := range v.Project.Tracks {
		var fields []gosx.Node
		keys := make([]string, 0, len(track.Params))
		for key := range track.Params {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := track.Params[key]
			text := value.Text
			if value.Number != nil {
				text = strconv.FormatFloat(*value.Number, 'f', -1, 64) + value.Unit
			}
			fields = append(fields, gosx.El("dt", gosx.Text(key)), gosx.El("dd", gosx.Text(text)))
		}
		cards = append(cards, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("h3", gosx.Text(track.ID+" · "+track.Kind)), gosx.El("dl", gosx.Fragment(fields...))))
	}
	for _, instrument := range v.Project.Instruments {
		var params []gosx.Node
		for _, p := range instrument.Params {
			params = append(params, gosx.El("li", gosx.Text(fmt.Sprintf("%s = %g%s", p.ID, p.Default, p.Unit))))
		}
		cards = append(cards, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip")), gosx.El("h3", gosx.Text(instrument.ID+" · "+instrument.Mode)), gosx.El("ul", gosx.Fragment(params...))))
	}
	return ui.Panel(ui.PanelProps{ID: "voices", Title: "Instruments", Description: "Declared voices and track overrides. Edit their definitions in Score."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "strips")), gosx.Fragment(cards...)))
}

func (s *studioApp) export(ctx *server.Context, v workspace, csrf string) gosx.Node {
	var status exportView
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/export", nil, &status); err != nil {
		return failure(err)
	}
	status.project()
	button := gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("disabled", status.Busy), gosx.Attr("data-gosx-live-bind-attr", "disabled:busy")), gosx.Text("Render WAV"))
	form := s.form(v, csrf, "export", "export", field("Target LUFS", numberInput("target_lufs", "-16", "-70", "0")), field("True peak ceiling (dBTP)", numberInput("true_peak_max", "-1", "-24", "0")), field("Tolerance (LU)", numberInput("tolerance", "1", "0", "10")), field("Sample rate", selectInput("rate", "48000", []string{"44100", "48000", "96000"})), field("PCM bits", selectInput("bits", "24", []string{"16", "24", "32"})), button)
	progress := gosx.El("section", gosx.Attrs(gosx.Attr("class", "export-result")), gosx.El("h3", gosx.Text("Render delivery")), gosx.El("p", gosx.Attrs(gosx.Attr("data-gosx-live-bind", "progress"), gosx.Attr("role", "status")), gosx.Text(status.Progress)), gosx.El("p", gosx.Attrs(gosx.Attr("data-gosx-live-bind", "quality")), gosx.Text(status.Quality)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-gosx-live-bind", "path")), gosx.Text(status.Path)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "error"), gosx.Attr("data-gosx-live-bind", "error")), gosx.Text(status.Error)), gosx.El("a", gosx.Attrs(gosx.Attr("href", status.DownloadURL), gosx.BoolAttr("download"), gosx.Attr("hidden", status.DownloadHidden), gosx.Attr("data-gosx-live-bind-attr", "href:downloadURL,hidden:downloadHidden"), gosx.Attr("data-gosx-live-bind", "downloadLabel")), gosx.Text(status.DownloadLabel)))
	return ui.Panel(ui.PanelProps{ID: "export", Title: "Render & export", Description: "Offline rendering uses the same score and kernel as Tymbal playback."}, gosx.El("div", gosx.Attrs(gosx.Attr("data-gosx-live-src", "/api/export"), gosx.Attr("data-gosx-live-interval", "1s")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), form), progress))
}
