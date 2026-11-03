package main

import (
	"fmt"
	"sort"
	"strconv"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

// modulationDock keeps source editing in the same revision-checked score and
// undo flow as the other automation controls.
func (s *studioApp) modulationDock(v workspace, csrf string) gosx.Node {
	if v.Project == nil {
		return gosx.Fragment()
	}
	p := v.Project
	if len(p.Tracks) == 0 && len(p.Modulators) == 0 && len(p.Routes) == 0 {
		return gosx.Fragment()
	}
	var tracks, sources, targets []string
	for _, track := range p.Tracks {
		tracks = append(tracks, track.ID)
	}
	for _, address := range project.ParamAddresses(p) {
		resolved, err := project.ResolveParameterPath(p, address.Address)
		if err == nil && resolved.OwnerKind == "track" && engine.RouteSupported(resolved.ID) {
			targets = append(targets, address.Address)
		}
	}
	sort.Strings(targets)
	form := func(children ...gosx.Node) gosx.Node {
		return s.form(v, csrf, "session", "automation", children...)
	}
	var sourceRows []gosx.Node
	for _, source := range p.Modulators {
		sources = append(sources, source.ID)
		detail := source.Kind
		switch {
		case source.Kind == "envelope":
			detail += " · follows " + source.Source
		case source.RateMicroHz > 0:
			detail += " · " + strconv.FormatFloat(float64(source.RateMicroHz)/1e6, 'f', -1, 64) + " Hz"
		case source.RateTicks > 0:
			detail += " · " + modulationPeriod(source.RateTicks)
		}
		sourceRows = append(sourceRows, gosx.El("li", gosx.El("strong", gosx.Text(source.ID)), gosx.Text(" · "+detail)))
	}
	if len(sourceRows) == 0 {
		sourceRows = append(sourceRows, gosx.El("li", gosx.Text("No modulation sources yet.")))
	}
	var rows []gosx.Node
	for _, route := range p.Routes {
		depth := automationValue(route.Depth)
		rows = append(rows, gosx.El("li", gosx.Attrs(gosx.Attr("data-mod-route", route.Source+":"+route.Target+":"+route.Mode)),
			form(hidden("action", "mod-route"), hidden("source", route.Source), hidden("target", route.Target), hidden("mode", route.Mode),
				gosx.El("strong", gosx.Text(route.Source+" → "+route.Target+" · "+route.Mode)),
				field("Depth", textInput("depth", depth)), submit("operation", "set", "Save depth"), submit("operation", "remove", "Remove route"))))
	}
	if len(rows) == 0 {
		rows = append(rows, gosx.El("li", gosx.Text("No routes yet.")))
	}
	var addRoute gosx.Node = gosx.Fragment()
	if len(sources) != 0 && len(targets) != 0 {
		addRoute = form(hidden("action", "mod-route"), hidden("operation", "add"),
			field("Source", selectInput("source", sources[0], sources)), field("Target", selectInput("target", targets[0], targets)),
			field("Mode", selectInput("mode", "add", []string{"add", "multiply"})), field("Depth", textInput("depth", "0")), submit("", "", "Add route"))
	}
	var addSource gosx.Node = gosx.Fragment()
	if len(tracks) != 0 {
		addSource = gosx.El("details", gosx.El("summary", gosx.Text("Add source")),
			form(hidden("action", "mod-source-add"), field("Name", textInput("name", "")),
				field("Kind", selectInput("sourcekind", "lfo", []string{"lfo", "sway", "contour", "envelope"})),
				field("Rate or contour period", textInput("rate", "1/4")), field("LFO waveform", selectInput("wave", "sine", []string{"sine", "triangle", "square", "sample-hold"})),
				field("Track to follow (envelope)", selectInput("source", tracks[0], tracks)), submit("", "", "Add source")))
	}
	return ui.Panel(ui.PanelProps{ID: "modulation-editor", Title: "Modulation", Description: "Create control sources and route them to track parameters. Changes save to the score and support Undo."},
		gosx.El("h3", gosx.Text("Sources")), gosx.El("ul", gosx.Fragment(sourceRows...)), addSource,
		gosx.El("h3", gosx.Text("Routes")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Routes apply in order. Add depth uses the target unit, such as 2dB or 500Hz; multiply uses a plain factor. Edit source settings and contour points in Code.")),
		gosx.El("ol", gosx.Fragment(rows...)), addRoute)
}

func modulationPeriod(ticks int64) string {
	if ticks%seq.TicksPerBar == 0 {
		bars := ticks / seq.TicksPerBar
		if bars == 1 {
			return "1 bar per cycle"
		}
		return fmt.Sprintf("%d bars per cycle", bars)
	}
	beats, remainder := ticks/seq.PPQ, ticks%seq.PPQ
	if remainder == 0 {
		if beats == 1 {
			return "1 beat per cycle"
		}
		return fmt.Sprintf("%d beats per cycle", beats)
	}
	a, b := ticks, seq.PPQ
	for b != 0 {
		a, b = b, a%b
	}
	if ticks < seq.PPQ {
		return fmt.Sprintf("%d/%d of a beat per cycle", ticks/a, seq.PPQ/a)
	}
	return fmt.Sprintf("%d/%d beats per cycle", ticks/a, seq.PPQ/a)
}
