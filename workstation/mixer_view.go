package main

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

// mixerChannel is one thing Device can open. A track, a bus, a return and the
// master each have a strip in the Mixer. An effect is a Device selection
// without a strip.
type mixerChannel struct {
	Kind  string // "track", "bus", "return", "master" or "effect"
	ID    string
	Name  string
	Label string // what the channel is: a track's instrument kind, "Bus", "Return", "Master" or an effect kind
	Meter string // the data-meter id, or "" when the engine has no meter for it
	Color int    // the track palette index, or -1
	Audio bool   // an audio track, which can take the vocal chain
	// Inserted marks an effect that sits on a channel's insert chain. Device
	// shows it with that channel; an effect on no chain has only its own entry.
	Inserted bool
	Strip    mixerStrip  // level, pan, mute, solo, routing, sends and inserts of a track, a bus or the master
	Effect   mixerReturn // the parameters of a return or an effect
}

// Ref names the channel in the deviceTrack query parameter: a track by its ID,
// anything else as "kind:id". A track ID never holds a colon.
func (c mixerChannel) Ref() string {
	if c.Kind == "track" {
		return c.ID
	}
	return c.Kind + ":" + c.ID
}

// Address is the public strip link address for a channel.
func (c mixerChannel) Address() string {
	if c.Kind == "master" {
		return "master"
	}
	return c.Ref()
}

// Title names the channel and what it is, such as "drums · analog kit" or
// "music · Bus". The master is only "Master".
func (c mixerChannel) Title() string {
	if strings.EqualFold(c.Name, c.Label) || c.Label == "" {
		return c.Name
	}
	return c.Name + " · " + c.Label
}

// mixerChannels lists the strips in the order the Mixer shows them (tracks,
// buses, returns, then the master) and the effects that are not returns.
func mixerChannels(mixer mixerState, p *project.Project) (strips, effects []mixerChannel) {
	kinds := map[string]string{}
	if p != nil {
		for _, track := range p.Tracks {
			kinds[track.ID] = track.Kind
		}
	}
	inserted := map[string]bool{}
	for index, strip := range mixer.Tracks {
		kind := strip.SourceKind
		if kind == "" {
			kind = kinds[strip.ID]
		}
		label := kind
		if label == "" {
			label = "Track"
		}
		strips = append(strips, mixerChannel{Kind: "track", ID: strip.ID, Name: strip.Name, Label: label, Meter: "track:" + strip.ID, Color: index % trackColors, Audio: kind == "audio", Strip: strip})
	}
	for _, strip := range mixer.Buses {
		strips = append(strips, mixerChannel{Kind: "bus", ID: strip.ID, Name: strip.Name, Label: "Bus", Meter: "bus:" + strip.ID, Color: -1, Strip: strip})
	}
	master := mixer.Master
	if master.ID == "" {
		master.ID = "master"
	}
	if master.Name == "" {
		master.Name = "Master"
	}
	for _, strip := range append(append(append([]mixerStrip(nil), mixer.Tracks...), mixer.Buses...), master) {
		for _, name := range strip.Inserts {
			inserted[name] = true
		}
	}
	returns := map[string]bool{}
	for _, ret := range mixer.Returns {
		returns[ret.Name] = true
	}
	for _, effect := range mergeMixerReturns(mixer.Returns, mixer.Effects) {
		// The service meters the delay return and the first reverb return.
		if returns[effect.Name] || effect.Kind == "tape" || effect.Meter != "" {
			meter := ""
			if effect.Meter != "" {
				meter = "return:" + effect.Meter
			}
			strips = append(strips, mixerChannel{Kind: "return", ID: effect.Name, Name: effect.Name, Label: "Return", Meter: meter, Color: -1, Effect: effect})
			continue
		}
		label := effect.Kind
		if label == "" {
			label = "Effect"
		}
		effects = append(effects, mixerChannel{Kind: "effect", ID: effect.Name, Name: effect.Name, Label: label, Color: -1, Inserted: inserted[effect.Name], Effect: effect})
	}
	strips = append(strips, mixerChannel{Kind: "master", ID: master.ID, Name: master.Name, Label: "Master", Meter: "master", Color: -1, Strip: master})
	return strips, effects
}

// deviceChannel is the channel Device shows. The deviceTrack query parameter
// names it: "kind:id" for a bus, the master, a return or an effect, otherwise
// a track ID. Without one, Device follows the inspected placement's track and
// then the first track (deviceTrack). ok is false when there is nothing to show.
func deviceChannel(p *project.Project, query url.Values, strips, effects []mixerChannel) (mixerChannel, bool) {
	address := query.Get("deviceTrack")
	if address == "master" {
		address = "master:master"
	}
	if kind, id, named := strings.Cut(address, ":"); named {
		for _, list := range [][]mixerChannel{strips, effects} {
			for _, c := range list {
				if c.Kind == kind && c.ID == id {
					return c, true
				}
			}
		}
		return mixerChannel{}, false
	}
	track := deviceTrack(p, query)
	id := query.Get("deviceTrack")
	if track != nil {
		id = track.ID
	}
	var first *mixerChannel
	for i := range strips {
		if strips[i].Kind != "track" {
			continue
		}
		if strips[i].ID == id {
			return strips[i], true
		}
		if first == nil {
			first = &strips[i]
		}
	}
	if track != nil {
		// The mixer state is missing; the project still names the track.
		return mixerChannel{Kind: "track", ID: track.ID, Name: track.ID, Label: track.Kind, Color: -1, Audio: track.Kind == "audio"}, true
	}
	if first != nil {
		return *first, true
	}
	return mixerChannel{}, false
}

// deviceSelection resolves the Device selection against the saved mixer state
// the workspace reads once per request, or the project alone without it.
func (v workspace) deviceSelection(query url.Values) (mixerChannel, bool) {
	var strips, effects []mixerChannel
	address := query.Get("deviceTrack")
	if v.mixer != nil && (strings.Contains(address, ":") || address == "master") {
		if mixer, err := v.mixer(); err == nil {
			strips, effects = mixerChannels(mixer, v.Project)
		}
	}
	return deviceChannel(v.Project, query, strips, effects)
}

// mixer renders the Mixer. In the Studio shell each strip shows only its
// direct controls and its name opens the channel in Device, which holds the
// exact-value, routing, insert and effect controls. The panel page served at
// "/" has no Device dock, so there the same controls stay under each strip.
func (s *studioApp) mixer(ctx *server.Context, v workspace, csrf string) gosx.Node {
	mixer, err := s.loadMixer(ctx, v)
	if err != nil {
		return failure(err)
	}
	v.Revision = mixer.Revision
	v.Edition = mixer.Edition
	channels, effects := mixerChannels(mixer, v.Project)
	query := ctx.Request.URL.Query()
	shell := ctx.Request.URL.Path == "/studio"
	// A strip's link names only the channel and the shell selections every
	// destination keeps, so the Mixer's markup does not change when another
	// view's selection does. The shell engine adds the rest of the page's
	// selections when the link is followed (shell_navigation_js.go).
	link := func(c mixerChannel) string {
		target := url.Values{}
		for _, name := range shellKept {
			if value := query.Get(name); value != "" {
				target.Set(name, value)
			}
		}
		target.Set("tab", "mixer")
		target.Set("dock", "device")
		target.Set("deviceTrack", c.Address())
		return "/studio?" + target.Encode()
	}
	chorusFields := []gosx.Node{hidden("source_revision", v.SourceRevision), hidden("action", "add-chorus"), field("Chorus name", textInput("name", "chorus")), submit("", "", "Create chorus")}
	if v.Edition == 1 {
		chorusFields = append(chorusFields, upgradeConfirm())
	}
	addChorus := s.form(v, csrf, "mixer", "mixer", chorusFields...)
	const description = "Track, bus, return, effect, and master parameters are saved to the score."

	if !shell {
		var strips []gosx.Node
		for _, c := range channels {
			var detail []gosx.Node
			if c.Kind == "return" {
				detail = s.effectControls(v, csrf, c.Effect)
			} else {
				controls := s.stripMixControls(v, csrf, c.Strip, true)
				detail = append(append(controls, s.stripInsertChain(v, csrf, c)...), s.vocalChainAction(v, csrf, c)...)
			}
			strips = append(strips, s.mixerStrip(v, csrf, c, "", false, detail))
		}
		for _, c := range effects {
			strips = append(strips, gosx.El("section", gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("data-mixer-strip", "effect"), gosx.Attr("id", "effect-"+c.ID)),
				gosx.El("h3", gosx.Text(c.Name)), gosx.Fragment(s.effectControls(v, csrf, c.Effect)...)))
		}
		return ui.Panel(ui.PanelProps{ID: "mixer", Title: "Mixer", Description: description}, addChorus, gosx.El("div", gosx.Attrs(gosx.Attr("class", "strips")), gosx.Fragment(strips...)))
	}

	selected := ""
	if channel, ok := deviceChannel(v.Project, query, channels, effects); ok {
		selected = channel.Ref()
	}
	var strips []gosx.Node
	for _, c := range channels {
		strips = append(strips, s.mixerStrip(v, csrf, c, link(c), c.Ref() == selected, nil))
	}
	// An effect on no insert chain has no strip and no channel that shows it,
	// so the tool strip lists it and opens it in Device.
	var loose []gosx.Node
	for _, c := range effects {
		if c.Inserted {
			continue
		}
		attrs := gosx.Attrs(gosx.Attr("href", link(c)), gosx.Attr("data-mixer-select", c.Address()), gosx.Attr("aria-label", "Open "+c.Name+" in Device"))
		if c.Ref() == selected {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		loose = append(loose, gosx.El("li", gosx.El("a", attrs, gosx.Text(c.Name)), gosx.Text(" "), gosx.El("span", gosx.Text(c.Label))))
	}
	var unplaced gosx.Node = gosx.Fragment()
	if len(loose) > 0 {
		unplaced = gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-unplaced")),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Effects on no channel. Open one to set it, or add it to an insert chain in Device.")),
			gosx.El("ul", gosx.Attrs(gosx.Attr("aria-label", "Effects on no channel")), gosx.Fragment(loose...)))
	}
	option := func(value, label string) gosx.Node {
		return gosx.El("option", gosx.Attrs(gosx.Attr("value", value)), gosx.Text(label))
	}
	// View controls: they change what this tab shows and never the score. The
	// stylesheet reads the chosen option, so they need no script and no request.
	// The id lets a page update keep the select, and with it the choice, when
	// a strip saves; data-studio-view-control keeps the choice from counting
	// as unsaved work.
	view := func(id, label string, options ...gosx.Node) gosx.Node {
		return gosx.El("label", gosx.Attrs(gosx.Attr("class", "field")), gosx.El("span", gosx.Text(label)),
			gosx.El("select", gosx.Attrs(gosx.Attr("id", id), gosx.Attr("data-"+id, ""), gosx.BoolAttr("data-studio-view-control")), gosx.Fragment(options...)))
	}
	tools := gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-tools"), gosx.Attr("role", "group"), gosx.Attr("aria-label", "Mixer tools")),
		view("mixer-show", "Show", option("all", "All channels"), option("tracks", "Tracks"), option("buses", "Buses and returns")),
		view("mixer-meter", "Meter", option("both", "Peak and RMS"), option("peak", "Peak"), option("rms", "RMS")),
		gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-mixer-reset-peaks", ""), gosx.Attr("title", "Clear the held peaks and clip marks on every meter")), gosx.Text("Reset peaks")),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "mixer-add-effect")), gosx.El("summary", gosx.Text("Add effect")),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-add-effect__body")), addChorus, s.addReturn(v, csrf, mixer), unplaced)))
	// The strips' forms return to "/?panel=mixer", which the shell turns into
	// this view with the Device selection kept (shellDestination).
	return ui.Panel(ui.PanelProps{ID: "mixer", Title: "Mixer", Description: description}, tools, gosx.El("div", gosx.Attrs(gosx.Attr("class", "strips")), gosx.Fragment(strips...)))
}

// mixerStrip is one channel strip. Every strip has the same rows in the same
// order, with an empty row where the channel has no such control, so the rows
// line up across strips: name, kind, inserts, sends, mute and solo, pan, then
// the meter with the fader and its dB readout. link, when set, makes the name
// open the channel in Device. detail is appended below the rows.
func (s *studioApp) mixerStrip(v workspace, csrf string, c mixerChannel, link string, selected bool, detail []gosx.Node) gosx.Node {
	attrs := gosx.Attrs(gosx.Attr("class", "strip"), gosx.Attr("data-mixer-strip", c.Kind), gosx.Attr("data-mixer-channel", c.Ref()))
	if c.Color >= 0 {
		attrs = append(attrs, gosx.Attr("data-track-color", strconv.Itoa(c.Color)))
	}
	if c.Kind == "return" && link == "" {
		// The panel page keeps a return's parameters under its strip, as it
		// does an effect's, and insert chain links point at this id.
		attrs = append(attrs, gosx.Attr("id", "effect-"+c.ID))
	}
	if selected {
		attrs = append(attrs, gosx.Attr("data-mixer-selected", "true"))
	}
	var name gosx.Node = gosx.Text(c.Name)
	if link != "" {
		linkAttrs := gosx.Attrs(gosx.Attr("href", link), gosx.Attr("data-mixer-select", c.Address()), gosx.Attr("aria-label", "Open "+c.Name+" in Device"))
		if selected {
			linkAttrs = append(linkAttrs, gosx.Attr("aria-current", "true"))
		}
		name = gosx.El("a", linkAttrs, gosx.Text(c.Name))
	}

	var inserts gosx.Node = gosx.El("p", gosx.Attrs(gosx.Attr("class", "strip-inserts strip-none")), gosx.Text("No inserts"))
	if len(c.Strip.Inserts) > 0 {
		var items []gosx.Node
		for _, insert := range c.Strip.Inserts {
			items = append(items, gosx.El("li", gosx.Text(insert)))
		}
		inserts = gosx.El("ol", gosx.Attrs(gosx.Attr("class", "strip-inserts"), gosx.Attr("aria-label", c.Name+" inserts")), gosx.Fragment(items...))
	}
	var sends gosx.Node = gosx.El("div", gosx.Attrs(gosx.Attr("class", "strip-sends")))
	if len(c.Strip.Sends) > 0 {
		var items []gosx.Node
		for _, send := range c.Strip.Sends {
			items = append(items, gosx.El("li", gosx.Text(sendText(send))))
		}
		sends = gosx.El("ul", gosx.Attrs(gosx.Attr("class", "strip-sends"), gosx.Attr("aria-label", c.Name+" sends")), gosx.Fragment(items...))
	}

	// Pan: the slider, or what stands in for it. Edition 1 confirms an upgrade
	// with each change, so it shows the saved value and edits it in Device.
	pan := c.Strip.Fields["pan"]
	panRow := s.mixerPan(v, csrf, c.Name, pan)
	if !mixerPanShows(v, pan) {
		var content gosx.Node = gosx.Fragment()
		if value, numeric := pan.Value.(float64); pan.Supported && numeric {
			content = gosx.El("span", gosx.Attrs(gosx.Attr("class", "strip-pan-readout")), gosx.Text(panText(value)))
		} else if pan.Path != "" {
			content = gosx.El("span", gosx.Attrs(gosx.Attr("class", "strip-none")), gosx.Text("No pan"))
		}
		panRow = gosx.El("div", gosx.Attrs(gosx.Attr("class", "strip-pan")), content)
	}

	var meter gosx.Node = gosx.El("span", gosx.Attrs(gosx.Attr("class", "strip-meter strip-meter--none"), gosx.Attr("title", "No meter"), gosx.Attr("aria-hidden", "true")))
	if c.Meter != "" {
		meter = stripMeter(c.Meter, c.Name)
	}
	level := c.Strip.Fields["level"]
	fader := s.mixerFader(v, csrf, c.Name, level)
	if !mixerFaderShows(v, level) {
		var content gosx.Node = gosx.Fragment()
		if level.Supported && level.Value != nil {
			content = gosx.El("span", gosx.Attrs(gosx.Attr("class", "strip-readout")), gosx.Text(levelText(level.Value)), gosx.El("small", gosx.Text(level.Descriptor.Unit)))
		} else if level.Path != "" {
			content = gosx.El("span", gosx.Attrs(gosx.Attr("class", "strip-none")), gosx.Text("No fader"))
		}
		fader = gosx.El("div", gosx.Attrs(gosx.Attr("class", "strip-fader")), content)
	}

	rows := []gosx.Node{
		gosx.El("h3", gosx.Attrs(gosx.Attr("class", "strip-name")), name),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "strip-kind")), gosx.Text(c.Label)),
		inserts,
		sends,
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "strip-toggles")), s.mixerToggle(v, csrf, "mixer", c.Name, "mute", c.Strip.Fields["mute"]), s.mixerToggle(v, csrf, "mixer", c.Name, "solo", c.Strip.Fields["solo"])),
		panRow,
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "strip-channel")), meter, fader),
	}
	if c.Kind == "master" {
		rows = append(rows, masterLoudness())
	}
	return gosx.El("section", attrs, gosx.Fragment(rows...), gosx.Fragment(detail...))
}

// levelText is a level for a readout: one decimal for a number, the saved
// word (such as "off") otherwise.
func levelText(value any) string {
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', 1, 64)
	}
	return fmt.Sprint(value)
}

var sendLevelUnit = regexp.MustCompile(`^(-?[0-9.]+)([A-Za-z]+)$`)

// sendText summarises one send for the strip face: where it goes and its
// level as the score writes it, such as "delay -12 dB" or "room 0.25 pre".
// Without the written level it shows the saved value.
func sendText(send mixerSend) string {
	level := sendLevelUnit.ReplaceAllString(send.Level, "$1 $2")
	if level == "" {
		switch value := send.Value.(type) {
		case float64:
			level = strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
			if unit := send.Descriptor.Unit; unit == "dB" {
				level += " " + unit
			}
		case nil:
		default:
			level = fmt.Sprint(value)
		}
	}
	text := strings.TrimSpace(send.To + " " + level)
	if send.Pre {
		text += " pre"
	}
	return text
}

// stripMixControls are the exact-value forms of a track, a bus or the master:
// every mixer field except the insert chain, then each send. A field the
// service does not support shows as not available.
func (s *studioApp) stripMixControls(v workspace, csrf string, strip mixerStrip, omitToggles bool) []gosx.Node {
	keys := make([]string, 0, len(strip.Fields))
	for key := range strip.Fields {
		if key != "insert" && !(omitToggles && (key == "mute" || key == "solo") && mixerToggleShows(v, strip.Fields[key])) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var controls []gosx.Node
	for _, key := range keys {
		controls = append(controls, s.mixerField(v, csrf, key, strip.Fields[key]))
	}
	for _, send := range strip.Sends {
		controls = append(controls, s.mixerField(v, csrf, "send to "+send.To, send.mixerField))
	}
	return controls
}

// stripInsertChain is the insert chain editor of a track, a bus or the master.
func (s *studioApp) stripInsertChain(v workspace, csrf string, c mixerChannel) []gosx.Node {
	insert, ok := c.Strip.Fields["insert"]
	switch {
	case !ok:
		return nil
	case !insert.Supported && c.Kind == "master" && len(c.Strip.Inserts) < 16:
		return nil
	case !insert.Supported:
		return []gosx.Node{mixerUnavailable("insert chain", insert.Reason)}
	}
	return []gosx.Node{s.effectChain(v, csrf, c.Strip)}
}

// vocalChainAction adds the vocal chain to an audio track.
func (s *studioApp) vocalChainAction(v workspace, csrf string, c mixerChannel) []gosx.Node {
	if !c.Audio {
		return nil
	}
	return []gosx.Node{
		s.form(v, csrf, "mixer", "mixer", hidden("action", "vocal-chain"), hidden("track", c.ID), submit("", "", "Add vocal chain")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("High-pass EQ, compression and de-essing, saved on this track.")),
	}
}

// effectControls are the exact-value forms of one effect's parameters.
func (s *studioApp) effectControls(v workspace, csrf string, effect mixerReturn) []gosx.Node {
	var controls []gosx.Node
	for _, f := range effect.Fields {
		controls = append(controls, s.mixerField(v, csrf, f.Path, f))
	}
	return controls
}

var mixerReasonCode = regexp.MustCompile(`^CICADA-[A-Z0-9-]+:?\s*`)

// mixerUnavailable shows a control the service does not support: its label, a
// plain "Not available" and the reason as a sentence. The reason as the
// service gave it, with its diagnostic code, stays in data-reason.
func mixerUnavailable(label, reason string) gosx.Node {
	var why gosx.Node = gosx.Fragment()
	if sentence := mixerReasonCode.ReplaceAllString(reason, ""); sentence != "" {
		first, size := utf8.DecodeRuneInString(sentence)
		why = gosx.El("small", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(string(unicode.ToUpper(first))+strings.TrimSuffix(sentence[size:], ".")+"."))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "mixer-control mixer-unavailable"), gosx.Attr("data-reason", reason)),
		gosx.El("p", gosx.El("span", gosx.Text(label)), gosx.Text(" "), gosx.El("strong", gosx.Text("Not available"))), why)
}

// upgradeConfirm is the checkbox an edition 1 score needs with each mixer change.
func upgradeConfirm() gosx.Node {
	return gosx.El("label", gosx.Attrs(gosx.Attr("class", "upgrade-confirm")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "confirmUpgrade"), gosx.BoolAttr("required"))), gosx.Text("Upgrade score to edition 2 for this change"))
}

// addSend offers returns that do not yet have a send from this track.
func (s *studioApp) addSend(v workspace, csrf string, c mixerChannel, mixer mixerState) gosx.Node {
	sent := map[string]bool{}
	for _, send := range c.Strip.Sends {
		sent[send.To] = true
	}
	var options []gosx.Node
	masterInserts := map[string]bool{}
	for _, name := range mixer.Master.Inserts {
		masterInserts[name] = true
	}
	for _, ret := range mergeMixerReturns(mixer.Returns, mixer.Effects) {
		if !masterInserts[ret.Name] && (ret.Kind == "delay" || ret.Kind == "tape" || ret.Kind == "reverb") && !sent[ret.Name] {
			options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", c.ID+".send."+ret.Name)), gosx.Text(ret.Name)))
		}
	}
	if len(options) == 0 {
		return gosx.Fragment()
	}
	fields := []gosx.Node{hidden("source_revision", v.SourceRevision), field("Send to", gosx.El("select", gosx.Attrs(gosx.Attr("name", "path")), gosx.Fragment(options...))), field("Level (dB)", textInput("value", "-12dB"))}
	if v.Edition == 1 {
		fields = append(fields, upgradeConfirm())
	}
	fields = append(fields, submit("", "", "Add send"))
	return s.form(v, csrf, "mixer", "mixer", fields...)
}
