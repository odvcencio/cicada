package main

import (
	"fmt"
	"sort"
	"strconv"

	"m31labs.dev/cicada/kernel"
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
		setting := func(name, label, value string, options []string) gosx.Node {
			input := textInput("value", value)
			if len(options) != 0 {
				input = selectInput("value", value, options)
			}
			return form(hidden("action", "mod-source-edit"), hidden("operation", "set"), hidden("source", source.ID), hidden("field", name), field(label, input), submit("", "", "Save "+label))
		}
		var settings []gosx.Node
		number := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		if source.Kind != "envelope" {
			fieldName := "rate"
			if source.Kind == "contour" {
				fieldName = "period"
			}
			settings = append(settings, setting(fieldName, "Rate or period", modulationRate(source), nil))
			settings = append(settings, setting("loop", "Loop", source.Loop, []string{"wrap", "continue"}))
		}
		switch source.Kind {
		case "lfo":
			settings = append(settings, setting("wave", "Waveform", source.Wave, []string{"sine", "triangle", "square", "sample-hold"}), setting("phase", "Phase", number(source.Phase), nil))
		case "sway":
			settings = append(settings, setting("octaves", "Octaves", strconv.Itoa(source.Octaves), []string{"1", "2", "3", "4"}))
		case "envelope":
			settings = append(settings, setting("source", "Follow track", source.Source, tracks), setting("attack", "Attack", number(source.AttackMS)+"ms", nil), setting("release", "Release", number(source.ReleaseMS)+"ms", nil))
		case "contour":
			var pointRows []gosx.Node
			for i, point := range source.Points {
				shape := gosx.Fragment()
				if i != 0 {
					shape = field("Shape", selectInput("shape", point.Shape, []string{"linear", "smooth", "step", "exponential"}))
				}
				var remove gosx.Node = gosx.Fragment()
				if i > 0 && i < len(source.Points)-1 {
					remove = submit("operation", "point-remove", "Remove point")
				}
				pointRows = append(pointRows, gosx.El("li", form(hidden("action", "mod-source-edit"), hidden("source", source.ID), hidden("index", strconv.Itoa(i)), targetField(contourPointKey(source.Points, i)), field("Position", textInput("at", number(point.At))), field("Value", textInput("value", number(point.Value))), shape, submit("operation", "point-set", "Save point"), remove)))
			}
			settings = append(settings, gosx.El("h4", gosx.Text("Contour points")), gosx.El("ol", gosx.Fragment(pointRows...)), form(hidden("action", "mod-source-edit"), hidden("operation", "point-add"), hidden("source", source.ID), field("Position", textInput("at", "0.5")), field("Value", textInput("value", "0")), field("Shape", selectInput("shape", "linear", []string{"linear", "smooth", "step", "exponential"})), submit("", "", "Add point")))
		}
		settings = append(settings, form(hidden("action", "mod-source-edit"), hidden("operation", "remove"), hidden("source", source.ID), submit("", "", "Remove source")))
		sourceRows = append(sourceRows, gosx.El("li", gosx.Attrs(gosx.Attr("data-mod-source", source.ID)), gosx.El("details", gosx.El("summary", gosx.El("strong", gosx.Text(source.ID)), gosx.Text(" · "+detail)), gosx.Fragment(settings...))))
	}
	if len(sources) != 0 {
		for _, address := range project.ParamAddresses(p) {
			id, ok := kernel.FindParam(address.Param)
			if !ok || address.Track == "" || !engine.RouteSupported(id) {
				continue
			}
			resolved, err := project.ResolveParameterPath(p, address.Address)
			if err == nil && resolved.OwnerKind == "track" && engine.RouteSupported(resolved.ID) {
				targets = append(targets, address.Address)
			}
		}
		sort.Strings(targets)
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
		gosx.El("h3", gosx.Text("Routes")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Routes apply in order. Add depth uses the target unit, such as 2dB or 500Hz; multiply uses a plain factor. Remove a source's routes before removing it.")),
		gosx.El("ol", gosx.Fragment(rows...)), addRoute)
}

func modulationRate(source project.Modulator) string {
	if source.RateMicroHz > 0 {
		return strconv.FormatFloat(float64(source.RateMicroHz)/1e6, 'f', -1, 64) + "Hz"
	}
	if bars, whole := (seq.MeterMap{}).WholeBars(0, source.RateTicks); whole {
		return fmt.Sprintf("%dbars", bars)
	}
	a, b := source.RateTicks, seq.TicksPerWhole
	for b != 0 {
		a, b = b, a%b
	}
	return fmt.Sprintf("%d/%d", source.RateTicks/a, seq.TicksPerWhole/a)
}

func modulationPeriod(ticks int64) string {
	if bars, whole := (seq.MeterMap{}).WholeBars(0, ticks); whole {
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
