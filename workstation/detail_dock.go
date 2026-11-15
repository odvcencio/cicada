package main

import (
	"fmt"
	"html"
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
// deviceTrack resolves the track; deviceChannel also resolves a bus, the
// master, a return or an effect, which the same query parameter names as
// "kind:id".
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
// revision guards. Scope their return targets to the channel being inspected,
// including when this hidden panel is revealed by the client-side dock tabs.
var dockReturnField = regexp.MustCompile(`(<input[^>]*name="__gosx_return_to"[^>]*value=")[^"]*("[^>]*>)`)

func dockForms(node gosx.Node, target string) gosx.Node {
	rendered := gosx.RenderHTML(node)
	return gosx.RawHTML(dockReturnField.ReplaceAllStringFunc(rendered, func(match string) string {
		parts := dockReturnField.FindStringSubmatch(match)
		return parts[1] + html.EscapeString(target) + parts[2]
	}))
}

// deviceDock shows one channel: a track's sound, mix, routing and inserts; a
// bus's or the master's mix, routing and inserts; a return's or an effect's
// parameters. It holds every exact-value control, so the Mixer strips keep only
// direct ones.
func (s *studioApp) deviceDock(ctx *server.Context, v workspace, csrf string) gosx.Node {
	query := ctx.Request.URL.Query()
	mixer, mixerErr := s.loadMixer(ctx, v)
	var strips, effects []mixerChannel
	if mixerErr == nil {
		strips, effects = mixerChannels(mixer, v.Project)
	}
	channel, ok := deviceChannel(v.Project, query, strips, effects)
	if !ok {
		description := "Add an instrument in the Browser to create a track, then shape its sound here."
		if strings.Contains(query.Get("deviceTrack"), ":") {
			description = "This channel no longer exists. Select a strip in the Mixer."
		}
		return ui.Panel(ui.PanelProps{ID: "device-editor", Title: "Device", Description: description})
	}
	query.Set("dock", "device")
	query.Set("deviceTrack", channel.Ref())
	target := "/studio?" + query.Encode()
	group := func(title string, children []gosx.Node) gosx.Node {
		return gosx.El("section", gosx.Attrs(gosx.Attr("class", "device-group"), gosx.Attr("aria-label", title)), gosx.El("h3", gosx.Text(title)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "device-controls")), gosx.Fragment(children...)))
	}
	note := func(text string) gosx.Node {
		return gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(text))
	}
	attrs := gosx.Attrs(gosx.Attr("class", "device-groups"), gosx.Attr("data-gosx-key", "device-"+channel.Ref()), gosx.Attr("data-device-channel", channel.Ref()))
	var groups []gosx.Node
	if channel.Kind == "track" {
		attrs = append(attrs, gosx.Attr("data-device-track", channel.ID))
		// Sound forms carry the workspace revision; the mixer forms below carry
		// the revision the mixer state was read at.
		groups = append(groups, group("Sound", s.deviceSound(v, csrf, channel.ID)))
	}
	if mixerErr == nil {
		v.Revision, v.Edition = mixer.Revision, mixer.Edition
	}
	switch {
	case mixerErr != nil:
		groups = append(groups, group("Mix and routing", []gosx.Node{failure(mixerErr)}))
	case channel.Kind == "return" || channel.Kind == "effect":
		title, controls := "Return effect", s.effectControls(v, csrf, channel.Effect)
		if channel.Kind == "effect" {
			title = "Effect"
			if !channel.Inserted {
				controls = append(controls, note("This effect is on no channel. Open a track in Device and add it to the insert chain."))
			}
		}
		if len(channel.Effect.Fields) == 0 {
			controls = append(controls, note("This effect has no parameters to set here."))
		}
		groups = append(groups, gosx.El("div", gosx.Attrs(gosx.Attr("id", "device-effect-"+channel.ID)), group(title, controls)))
	default:
		controls := s.stripMixControls(v, csrf, channel.Strip, false)
		if channel.Kind == "track" {
			controls = append(controls, s.addSend(v, csrf, channel, mixer))
		}
		if len(controls) == 0 {
			controls = append(controls, note("No mixer controls are available for this channel."))
		}
		inserts := s.stripInsertChain(v, csrf, channel)
		inserts = append(inserts, s.addInsert(v, csrf, channel))
		if channel.Kind == "track" {
			inserts = append(inserts, ampAutomationLinks(ctx, v.Project, channel.ID))
		}
		inserts = append(inserts, s.vocalChainAction(v, csrf, channel)...)
		for _, name := range channel.Strip.Inserts {
			for _, effect := range mixer.Effects {
				if effect.Name == name {
					inserts = append(inserts, gosx.El("details", gosx.Attrs(gosx.Attr("class", "device-effect"), gosx.Attr("id", "device-effect-"+name)), gosx.El("summary", gosx.Text(name)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "device-controls")), gosx.Fragment(s.effectControls(v, csrf, effect)...))))
				}
			}
		}

		groups = append(groups, group("Mix and routing", controls), group("Insert effects", inserts))
	}
	content := ui.Panel(ui.PanelProps{ID: "device-editor", Title: channel.Title(), Description: "Channel controls, instrument settings and effects. Changes save to the score and support Undo."},
		devicePicker(v.Project, query, channel, strips, effects), gosx.El("div", attrs, gosx.Fragment(groups...)))
	// Chain links target the Device copy of an effect's controls.
	rendered := gosx.RenderHTML(dockForms(content, target))
	rendered = strings.ReplaceAll(rendered, `href="#effect-`, `href="#device-effect-`)
	return gosx.RawHTML(rendered)
}

// devicePicker chooses the channel Device shows: every strip in the Mixer, and
// each effect that sits on no insert chain. It keeps the rest of the page's
// selections.
func devicePicker(p *project.Project, query url.Values, selected mixerChannel, strips, effects []mixerChannel) gosx.Node {
	names := make([]string, 0, len(query))
	for name := range query {
		if name != "deviceTrack" && name != "placement" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var preserved []gosx.Node
	for _, name := range names {
		for _, value := range query[name] {
			preserved = append(preserved, hidden(name, value))
		}
	}
	groups := []struct {
		label   string
		options []gosx.Node
	}{{label: "Tracks"}, {label: "Buses and master"}, {label: "Returns"}, {label: "Effects on no channel"}}
	add := func(group int, c mixerChannel) {
		attrs := gosx.Attrs(gosx.Attr("value", c.Ref()))
		if c.Ref() == selected.Ref() {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		groups[group].options = append(groups[group].options, gosx.El("option", attrs, gosx.Text(c.Name)))
	}
	for _, c := range strips {
		switch c.Kind {
		case "track":
			add(0, c)
		case "return":
			add(2, c)
		default:
			add(1, c)
		}
	}
	if len(groups[0].options) == 0 && p != nil {
		// The mixer state is missing; the project still names the tracks.
		for _, track := range p.Tracks {
			add(0, mixerChannel{Kind: "track", ID: track.ID, Name: track.ID})
		}
	}
	for _, c := range effects {
		if !c.Inserted || c.Ref() == selected.Ref() {
			add(3, c)
		}
	}
	var options []gosx.Node
	for _, g := range groups {
		if len(g.options) > 0 {
			options = append(options, gosx.El("optgroup", gosx.Attrs(gosx.Attr("label", g.label)), gosx.Fragment(g.options...)))
		}
	}
	return gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/studio"), gosx.Attr("class", "device-track-picker")),
		gosx.Fragment(preserved...), field("Channel", gosx.El("select", gosx.Attrs(gosx.Attr("name", "deviceTrack"), gosx.Attr("aria-label", "Device channel")), gosx.Fragment(options...))), submit("", "", "Inspect channel"))
}

// deviceSound is a track's instrument controls: the declared instrument's
// parameters, or the built-in voice's.
func (s *studioApp) deviceSound(v workspace, csrf, id string) []gosx.Node {
	var track *project.Track
	if v.Project != nil {
		for i := range v.Project.Tracks {
			if v.Project.Tracks[i].ID == id {
				track = &v.Project.Tracks[i]
			}
		}
	}
	if track == nil {
		return []gosx.Node{gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Instrument controls are unavailable for this track."))}
	}
	var controls []gosx.Node
	for i := range v.Project.Instruments {
		definition := &v.Project.Instruments[i]
		if definition.ID == track.Kind {
			controls = s.instrumentTrackParameters(v, csrf, *track, definition)
			break
		}
	}
	if len(controls) == 0 {
		controls = s.builtinTrackParameters(v, csrf, *track)
	}
	if len(controls) == 0 {
		text := "Instrument controls and routing are saved on this track. Add instruments in the Browser."
		if track.Kind == "audio" {
			text = "Recorded audio track. Record and comp takes in Arrange; shape the vocal with its insert chain here."
		}
		controls = append(controls, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(text)))
	}
	return controls
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
	modulation := s.modulationDock(v, csrf)
	if gosx.RenderHTML(curve) == "" && gosx.RenderHTML(scenes) == "" && gosx.RenderHTML(modulation) == "" {
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
	node := shellLinks(&current, gosx.Fragment(curve, modulation, scenes))
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
