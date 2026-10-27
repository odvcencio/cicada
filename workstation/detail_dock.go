package main

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

// Device selection follows an inspected placement until the user explicitly
// chooses a track. A subsequent placement link supplies a fresh selection.
func deviceTrack(p *project.Project, query url.Values) *project.Track {
	if p == nil || len(p.Tracks) == 0 {
		return nil
	}
	selected := query.Get("deviceTrack")
	if selected == "" && p.Arrange != nil {
		for _, placement := range p.Arrange.Placements {
			if placement.ID == query.Get("placement") {
				selected = placement.Track
				break
			}
		}
	}
	for i := range p.Tracks {
		if p.Tracks[i].ID == selected {
			return &p.Tracks[i]
		}
	}
	return &p.Tracks[0]
}

// Shared instrument, mixer and chain forms keep their domain actions and
// revision guards. Scope their return targets to the track being inspected,
// including when this hidden panel is revealed by the client-side dock tabs.
var dockReturnField = regexp.MustCompile(`(<input[^>]*name="__gosx_return_to"[^>]*value=")[^"]*("[^>]*>)`)

func dockForms(node gosx.Node, target string) gosx.Node {
	rendered := gosx.RenderHTML(node)
	return gosx.RawHTML(dockReturnField.ReplaceAllStringFunc(rendered, func(match string) string {
		parts := dockReturnField.FindStringSubmatch(match)
		return parts[1] + html.EscapeString(target) + parts[2]
	}))
}

func (s *studioApp) deviceDock(ctx *server.Context, v workspace, csrf string) gosx.Node {
	track := deviceTrack(v.Project, ctx.Request.URL.Query())
	if track == nil {
		return ui.Panel(ui.PanelProps{ID: "device-editor", Title: "Device", Description: "Add an instrument in the Browser to create a track, then shape its sound here."})
	}
	query := ctx.Request.URL.Query()
	query.Set("dock", "device")
	query.Set("deviceTrack", track.ID)
	target := "/studio?" + query.Encode()
	var choices []string
	for _, t := range v.Project.Tracks {
		choices = append(choices, t.ID)
	}
	var preserved []gosx.Node
	for name, values := range query {
		if name == "deviceTrack" || name == "placement" {
			continue
		}
		for _, value := range values {
			preserved = append(preserved, hidden(name, value))
		}
	}
	picker := gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/studio"), gosx.Attr("class", "device-track-picker")),
		gosx.Fragment(preserved...), field("Track", selectInput("deviceTrack", track.ID, choices, "Device track")), submit("", "", "Inspect track"))
	var sourceControls []gosx.Node
	for i := range v.Project.Instruments {
		definition := &v.Project.Instruments[i]
		if definition.ID == track.Kind {
			sourceControls = s.instrumentTrackParameters(v, csrf, *track, definition)
			break
		}
	}
	if len(sourceControls) == 0 {
		sourceControls = s.builtinTrackParameters(v, csrf, *track)
	}
	if len(sourceControls) == 0 {
		text := "Instrument controls and routing are saved on this track. Add instruments in the Browser."
		if track.Kind == "audio" {
			text = "Recorded audio track. Record and comp takes in Arrange; shape the vocal with its insert chain here."
		}
		sourceControls = append(sourceControls, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(text)))
	}
	var controls, effects []gosx.Node
	var mixer mixerState
	if err := s.backend.call(ctx.Request.Context(), http.MethodGet, "/api/mixer", nil, &mixer); err != nil {
		controls = append(controls, failure(err))
	} else {
		v.Revision, v.Edition = mixer.Revision, mixer.Edition
		for _, strip := range mixer.Tracks {
			if strip.ID != track.ID {
				continue
			}
			var keys []string
			for key, f := range strip.Fields {
				if f.Supported && key != "insert" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				controls = append(controls, s.mixerField(v, csrf, key, strip.Fields[key]))
			}
			for _, send := range strip.Sends {
				controls = append(controls, s.mixerField(v, csrf, "send to "+send.To, send.mixerField))
			}
			if insert, ok := strip.Fields["insert"]; ok && insert.Supported {
				effects = append(effects, s.effectChain(v, csrf, strip))
			}
			effects = append(effects, ampAutomationLinks(ctx, v.Project, track.ID))
			if track.Kind == "audio" {
				effects = append(effects, s.form(v, csrf, "mixer", "mixer", hidden("action", "vocal-chain"), hidden("track", track.ID), submit("", "", "Add vocal chain")))
			}
			for _, name := range strip.Inserts {
				for _, effect := range mixer.Effects {
					if effect.Name == name {
						var fields []gosx.Node
						for _, f := range effect.Fields {
							fields = append(fields, s.mixerField(v, csrf, f.Path, f))
						}
						effects = append(effects, gosx.El("details", gosx.Attrs(gosx.Attr("class", "device-effect"), gosx.Attr("id", "device-effect-"+name)), gosx.El("summary", gosx.Text(name)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "device-controls")), gosx.Fragment(fields...))))
					}
				}
			}
			break
		}
	}
	if len(controls) == 0 {
		controls = append(controls, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("No mixer controls are available for this track.")))
	}
	if len(effects) == 0 {
		effects = append(effects, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("This track has no insert chain. Add EQ, compression or drive effects in the score to make them available here.")))
	}
	group := func(title string, children []gosx.Node) gosx.Node {
		return gosx.El("section", gosx.Attrs(gosx.Attr("class", "device-group"), gosx.Attr("aria-label", title)), gosx.El("h3", gosx.Text(title)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "device-controls")), gosx.Fragment(children...)))
	}
	content := ui.Panel(ui.PanelProps{ID: "device-editor", Title: track.ID + " · " + track.Kind, Description: "Track controls, instrument settings and effects. Changes save to the score and support Undo."},
		picker, gosx.El("div", gosx.Attrs(gosx.Attr("class", "device-groups"), gosx.Attr("data-device-track", track.ID)), group("Sound", sourceControls), group("Mix and routing", controls), group("Insert effects", effects)))
	// Chain links target the Device copy of an effect's controls, rather than
	// the same effect in the hidden Mixer panel.
	rendered := gosx.RenderHTML(dockForms(content, target))
	rendered = strings.ReplaceAll(rendered, `href="#effect-`, `href="#device-effect-`)
	return gosx.RawHTML(rendered)
}

// Built-in source controls share the saved instrument action with authored
// controls. Mixer descriptors stay in Mix and routing; sampled/custom kits
// keep their own editing surface.
func (s *studioApp) builtinTrackParameters(v workspace, csrf string, track project.Track) []gosx.Node {
	for _, kit := range v.Project.Kits {
		if kit.ID == track.Kind {
			return nil
		}
	}
	defaults := *v.Project
	defaults.Tracks = append([]project.Track(nil), v.Project.Tracks...)
	for i := range defaults.Tracks {
		if defaults.Tracks[i].ID == track.ID {
			defaults.Tracks[i].Params = nil
		}
	}
	defaultValues := map[string]any{}
	for _, address := range project.ParamAddresses(&defaults) {
		if address.Track == track.ID {
			defaultValues[address.Param] = address.Value
		}
	}
	// Put expressive sound controls in the visible first row, followed by
	// other timbre settings and shared pitch controls. Each group retains
	// catalog order; lane-specific drum tuning stays with its drum sounds.
	var groups [3][]gosx.Node
	seen := map[string]bool{}
	for _, address := range project.ParamAddresses(v.Project) {
		if address.Track != track.ID || strings.HasPrefix(address.Param, "mix.") || strings.HasPrefix(address.Param, "amp.") {
			continue
		}
		descriptor, ok := project.LookupParamDescriptor(address.Param)
		if !ok || descriptor.Persist != "source" || seen[descriptor.Source] || descriptor.ID == "guitar.experimental" {
			continue
		}
		seen[descriptor.Source] = true
		value := builtinControlValue(descriptor, address.Value)
		defaultValue := builtinControlValue(descriptor, defaultValues[address.Param])
		label := descriptor.Source + " · default"
		if _, overridden := track.Params[descriptor.Source]; overridden {
			label = descriptor.Source + " · track override"
		}
		if descriptor.Unit != "" && descriptor.Unit != "unit" {
			label += " (" + descriptor.Unit + ")"
		}
		var input gosx.Node
		help := ""
		if descriptor.Curve == "enum" || descriptor.Curve == "toggle" {
			choices := descriptor.Values
			if len(choices) == 0 {
				choices = []string{"off", "on"}
			}
			input = selectInput("value", value, choices)
			help = "Choices: " + strings.Join(choices, ", ") + " · Default: " + defaultValue
		} else {
			step := "any"
			if descriptor.Type == "integer" || descriptor.Curve == "integer" {
				step = "1"
			}
			input = gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", "value"), gosx.Attr("value", value), gosx.Attr("min", descriptor.Min), gosx.Attr("max", descriptor.Max), gosx.Attr("step", step)))
			defaultText := defaultValue + instrumentUnit(descriptor.Unit)
			help = paramdefs.FormatBound(descriptor, descriptor.Min, false) + " to " + paramdefs.FormatBound(descriptor, descriptor.Max, true) + " · Default: " + defaultText
		}
		reset := gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "reset-parameter"), gosx.BoolAttr("formnovalidate")), gosx.Text("Use default"))
		form := s.form(v, csrf, "instruments", "instrument", hidden("track", track.ID), hidden("newName", descriptor.Source), field(label, input), submit("action", "set-parameter", "Apply"), reset)
		group := 1
		if descriptor.Live && descriptor.Automatable {
			group = 0
		}
		switch descriptor.Source {
		case "tune", "fine", "octave":
			group = 2
		}
		groups[group] = append(groups[group], gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-control"), gosx.Attr("data-device-parameter", track.ID+"."+descriptor.Source), gosx.Attr("data-gosx-key", "builtin-parameter-"+track.ID+"-"+descriptor.Source)), form, gosx.El("small", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(help))))
	}
	return append(append(groups[0], groups[1]...), groups[2]...)
}

func builtinControlValue(descriptor paramdefs.Descriptor, value any) string {
	if number, ok := value.(float64); ok {
		if descriptor.Curve == "enum" || descriptor.Curve == "toggle" {
			choices := descriptor.Values
			if len(choices) == 0 {
				choices = []string{"off", "on"}
			}
			if index := int(number); index >= 0 && index < len(choices) {
				return choices[index]
			}
		}
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}

func (s *studioApp) automationDock(ctx *server.Context, v workspace, csrf string) gosx.Node {
	curve := s.continuousAutomation(ctx, v, csrf)
	scenes := s.automation(ctx, v, csrf)
	if gosx.RenderHTML(curve) == "" && gosx.RenderHTML(scenes) == "" {
		description := "Place a pattern or audio clip in the song to begin automating track and effect parameters."
		if v.Project.Edition < 2 {
			description = "This score uses scene automation. Add a scene and song section to set parameter changes at scene boundaries; edition 2 also supports drawn curves."
		}
		return ui.Panel(ui.PanelProps{ID: "automation-empty", Title: "Automation", Description: description})
	}
	// The panel is rendered even while hidden: its forms must return to this
	// dock after an in-place tab switch, and retain the chosen parameter.
	current := *ctx.Request.URL
	query := current.Query()
	query.Set("dock", "automation")
	current.RawQuery = query.Encode()
	node := shellLinks(&current, gosx.Fragment(curve, scenes))
	rendered := gosx.RenderHTML(node)
	rendered = dockReturnField.ReplaceAllStringFunc(rendered, func(match string) string {
		parts := dockReturnField.FindStringSubmatch(match)
		target, err := url.Parse(html.UnescapeString(strings.TrimSuffix(strings.TrimPrefix(match, parts[1]), parts[2])))
		if err != nil {
			return match
		}
		value := shellDestination(&current, target.String(), true)
		return parts[1] + html.EscapeString(value) + parts[2]
	})
	return gosx.RawHTML(rendered)
}
