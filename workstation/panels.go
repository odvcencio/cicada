package main

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"m31labs.dev/cicada/language"
	"m31labs.dev/cicada/project"
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
		return s.session(v, csrf)
	case "patterns":
		return s.patterns(v, csrf)
	case "generator":
		return s.generator(ctx, v, csrf)
	case "voices":
		return s.voices(v)
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
	buttons := []editor.FormButton{{Label: "Save score", Class: "primary"}}
	var conflict gosx.Node = gosx.Fragment()
	if v.HasDraft && v.Revision != v.DiskRevision {
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
	e := editor.New("source-editor", editor.Options{
		Surface: editor.SurfaceCode, Content: v.Source, Title: v.Filename, Label: "Cicada score", Language: editor.Lang("cicada"), Theme: editor.ThemeDark,
		FormAction: "/__actions/source", CSRFToken: csrf,
		ExtraFields: map[string]string{"revision": v.Revision, "diskRevision": v.DiskRevision, action.ReturnTargetField: "/?panel=code"},
		Code:        &editor.CodeOptions{Language: "cicada", TabWidth: 2, InsertSpaces: true, Gutter: true, Highlights: highlights},
		Buttons:     buttons,
		Panels:      []editor.Panel{},
	})
	return ui.Panel(ui.PanelProps{ID: "score", Title: "Score", Description: "Changes validate before saving and land at the next bar."}, conflict, e.Render())
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
	var song []gosx.Node
	bar := 1
	for index, entry := range p.Song {
		i := strconv.Itoa(index)
		controls := []gosx.Node{s.form(v, csrf, "session", "transport", hidden("entry", i), submit("action", "playFrom", "Play from here")),
			s.form(v, csrf, "session", "song", hidden("action", "bars"), hidden("index", i), field("Bars", gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", "bars"), gosx.Attr("value", strconv.Itoa(int(entry.Bars))), gosx.Attr("min", "1"), gosx.Attr("max", "64")))), submit("", "", "Set bars"))}
		if index > 0 {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), hidden("target", strconv.Itoa(index-1)), submit("", "", "Move earlier")))
		}
		if index+1 < len(p.Song) {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), hidden("target", strconv.Itoa(index+1)), submit("", "", "Move later")))
		}
		song = append(song, gosx.El("li", gosx.El("h3", gosx.Text(fmt.Sprintf("%s · bars %d–%d", entry.Scene, bar, bar+int(entry.Bars)-1))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.Fragment(controls...))))
		bar += int(entry.Bars)
	}
	return gosx.Fragment(ui.Panel(ui.PanelProps{ID: "session", Title: "Scene launch matrix", Description: "Scene launches are quantized to the next bar."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Scene launch matrix")), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...))))), ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: "Source-backed scene order and bar lengths."}, gosx.El("ol", gosx.Fragment(song...))))
}

var drumOrder = []string{"bd", "sd", "ch", "oh", "cp", "rs", "lt", "mt", "ht", "cb", "cy"}

func (s *studioApp) patterns(v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return failure(fmt.Errorf("save a valid score to edit patterns"))
	}
	var cards []gosx.Node
	for _, p := range v.Project.Patterns {
		var lanes []gosx.Node
		if p.Kind == "drums" {
			for _, lane := range drumOrder {
				if steps, ok := p.Lanes[lane]; ok {
					lanes = append(lanes, s.lane(v, csrf, p, lane, steps))
				}
			}
		} else {
			lanes = append(lanes, s.lane(v, csrf, p, "", p.Data))
		}
		// Every modifier uses the same typed Go action and source patcher.
		modifier := s.form(v, csrf, "patterns", "toggle", hidden("pattern", p.ID), field("Step (zero based)", gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", "step"), gosx.Attr("min", "0"), gosx.Attr("max", strconv.Itoa(int(p.Steps)-1)), gosx.Attr("value", "0")))), field("Lane", textInput("lane", "")), field("Modifier", selectInput("modifier", "accent", []string{"accent", "slide", "tie", "ratchet", "chance"})), submit("", "", "Apply modifier"))
		pitch := gosx.Fragment()
		if p.Kind != "drums" {
			pitch = s.form(v, csrf, "patterns", "toggle", hidden("pattern", p.ID), field("Step (zero based)", numberInput("step", "0", "0", strconv.Itoa(int(p.Steps)-1))), field("MIDI pitch", numberInput("pitch", "60", "0", "127")), submit("", "", "Set pitch"))
		}
		cards = append(cards, gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-card"), gosx.BoolAttr("open"), gosx.Attr("data-pattern", p.ID)), gosx.El("summary", gosx.Text(fmt.Sprintf("%s · %s · %d steps", p.ID, p.Kind, p.Steps))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.Fragment(lanes...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), modifier, pitch)))
	}
	return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Patterns", Description: "Toggle a step, set its pitch, or edit articulation."}, gosx.Fragment(cards...))
}

func (s *studioApp) lane(v workspace, csrf string, p project.Pattern, lane string, steps []*project.Step) gosx.Node {
	label := strings.ToUpper(lane)
	if label == "" {
		label = "NOTES"
	}
	children := []gosx.Node{hidden("pattern", p.ID), hidden("lane", lane), gosx.El("span", gosx.Attrs(gosx.Attr("class", "lane-label")), gosx.Text(label))}
	for index := 0; index < int(p.Steps); index++ {
		var step *project.Step
		if index < len(steps) {
			step = steps[index]
		}
		text := "·"
		if step != nil {
			if p.Kind == "drums" {
				text = "×"
			} else {
				text = strconv.Itoa(int(step.Note))
			}
			if step.Tie {
				text = "—"
			}
			if step.Accent {
				text += "^"
			}
			if step.Slide {
				text += "~"
			}
			if step.Ratchet > 1 {
				text += fmt.Sprintf("×%d", step.Ratchet)
			}
		}
		children = append(children, ui.Step(ui.StepProps{Label: fmt.Sprintf("%s %s step %d: %s", p.ID, label, index+1, text), Text: text, Active: step != nil, Index: index, Pattern: p.ID, Lane: lane}))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "lane")), s.form(v, csrf, "patterns", "toggle", children...))
}

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
	var status struct {
		State string `json:"state"`
		Path  string `json:"path"`
		Error string `json:"error"`
		Pass  int    `json:"pass"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/export", nil, &status); err != nil {
		return failure(err)
	}
	form := s.form(v, csrf, "export", "export", field("Target LUFS", numberInput("target_lufs", "-16", "-36", "-5")), field("True peak ceiling (dBTP)", numberInput("true_peak_max", "-1", "-12", "0")), field("Tolerance (LU)", numberInput("tolerance", "1", "0.1", "2")), field("Sample rate", selectInput("rate", "48000", []string{"44100", "48000", "96000"})), field("PCM bits", selectInput("bits", "24", []string{"16", "24", "32"})), submit("", "", "Render WAV"))
	progress := gosx.El("div", gosx.Attrs(gosx.Attr("data-gosx-live-src", "/api/export"), gosx.Attr("data-gosx-live-interval", "1s")), gosx.El("p", gosx.Attrs(gosx.Attr("data-gosx-live-bind", "state")), gosx.Text(status.State)), gosx.El("p", gosx.Attrs(gosx.Attr("data-gosx-live-bind", "path")), gosx.Text(status.Path)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "error"), gosx.Attr("data-gosx-live-bind", "error")), gosx.Text(status.Error)))
	return ui.Panel(ui.PanelProps{ID: "export", Title: "Render & export", Description: "Offline rendering uses the same score and kernel as Tymbal playback."}, form, progress)
}
