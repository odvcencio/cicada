package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

type automationCurvePoint struct {
	Tick  int64   `json:"tick"`
	Value float64 `json:"value"`
	Shape string  `json:"shape"`
	Curve float64 `json:"curve"`
}
type automationCurveProps struct {
	Path, Unit, Scale string
	Min, Max          float64
	End               int64
	Points            []automationCurvePoint
}

func (s *studioApp) continuousAutomation(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil || v.Project.Edition < 2 {
		return gosx.Fragment()
	}
	p := v.Project
	var paths []string
	for _, a := range project.ParamAddresses(p) {
		r, err := project.ResolveParameterPath(p, a.Address)
		if err == nil && r.Descriptor.Live && r.Descriptor.Automatable && r.Descriptor.Unit != "enum" && r.Descriptor.Curve != "toggle" {
			paths = append(paths, a.Address)
		}
	}
	if len(paths) == 0 {
		return gosx.Fragment()
	}
	selected := ctx.Request.URL.Query().Get("curvePath")
	found := false
	for _, path := range paths {
		if path == selected {
			found = true
		}
	}
	if !found {
		selected = paths[0]
		for _, path := range paths {
			if path == "danilo.level" || path == "vox.level" {
				selected = path
				break
			}
		}
	}
	r, _ := project.ResolveParameterPath(p, selected)
	d := r.Descriptor
	var end int64
	if p.Arrange != nil {
		for _, place := range p.Arrange.Placements {
			end = max(end, place.AtTick+place.LengthTicks)
		}
	} else {
		for _, entry := range p.Song {
			end += int64(entry.Bars) * 3840
		}
	}
	if end <= 0 {
		return gosx.Fragment()
	}
	props := automationCurveProps{Path: selected, Unit: automationUnit(d.Unit), Scale: d.Curve, Min: d.Min, Max: d.Max, End: end, Points: []automationCurvePoint{}}
	for _, lane := range p.Automation {
		if lane.Path == selected {
			for _, point := range lane.Points {
				if point.Value.Number != nil {
					props.Points = append(props.Points, automationCurvePoint{point.Tick, *point.Value.Number, point.Shape, point.Curve})
				}
			}
		}
	}
	value := d.Min
	for _, a := range project.ParamAddresses(p) {
		if a.Address == selected {
			if n, ok := a.Value.(float64); ok {
				value = math.Max(d.Min, math.Min(d.Max, n))
			}
		}
	}
	at := int64(0)
	shape := "linear"
	tension := 0.0
	if len(props.Points) > 0 {
		first := props.Points[0]
		value, at, shape, tension = first.Value, first.Tick, first.Shape, first.Curve
	}
	returnTo := "/?panel=session&curvePath=" + url.QueryEscape(selected)
	chooser := gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("class", "action-form")), hidden("panel", "session"), field("Automation parameter", selectInput("curvePath", selected, paths, "Automation parameter")), submit("", "", "Show curve"))
	pointForm := ui.Form(ui.FormProps{Action: "/__actions/automation", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form automation-point-form"},
		hidden("path", selected), hidden("oldAt", map[bool]string{true: vocalPosition(at), false: ""}[len(props.Points) > 0]),
		field("Point position", textInput("at", vocalPosition(at))), field("Point value", textInput("value", strconv.FormatFloat(value, 'f', -1, 64)+props.Unit)),
		field("Transition", selectInput("shape", shape, []string{"linear", "smooth", "step", "exponential", "curve"}, "Transition")),
		field("Curve tension", gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", "curve"), gosx.Attr("aria-label", "Curve tension"), gosx.Attr("min", -8), gosx.Attr("max", 8), gosx.Attr("step", "0.25"), gosx.Attr("value", tension)))),
		submit("action", "automation-point-set", "Save point"), submit("action", "automation-point-remove", "Remove selected point"))
	graph := gosx.El("div", gosx.Attrs(gosx.Attr("class", "automation-curve-scroll")), gosx.El("svg", gosx.Attrs(gosx.Attr("class", "automation-curve"), gosx.Attr("data-automation-curve", ""), gosx.Attr("viewBox", "0 0 1000 240"), gosx.Attr("preserveAspectRatio", "none"), gosx.Attr("role", "group"), gosx.Attr("aria-label", "Automation curve for "+selected), gosx.Attr("tabindex", "0")),
		gosx.El("path", gosx.Attrs(gosx.Attr("data-automation-line", ""))), gosx.El("g", gosx.Attrs(gosx.Attr("data-automation-points", "")))))
	content := gosx.Fragment(chooser, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(fmt.Sprintf("%s · %g to %g %s. Double-click the curve to add a point; drag a point to move it. Arrow keys adjust it; Escape cancels a drag.", selected, d.Min, d.Max, props.Unit))), graph,
		gosx.El("p", gosx.Attrs(gosx.Attr("data-automation-curve-status", ""), gosx.Attr("role", "status")), gosx.Text("Select a point, or add one with the controls below.")),
		gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-automation-new", "")), gosx.Text("New point")), pointForm,
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Transitions describe the segment arriving at each point. Save records one Undo step. Curves override scene settings after their first point.")))
	encoded, _ := json.Marshal(props)
	surface := ctx.Engine(engine.Config{Name: "CicadaAutomationCurve", Kind: engine.KindSurface, MountID: "cicada-automation-curve", Runtime: engine.RuntimeGoWASM, WASMPath: ui.MeterEnginePath, Props: encoded, Capabilities: []engine.Capability{engine.CapPointer, engine.CapKeyboard}, RequiredCapabilities: []engine.Capability{engine.CapWASM}}, gosx.El("div", gosx.Attrs(gosx.Attr("data-automation-curve-props", string(encoded))), content))
	return ui.Panel(ui.PanelProps{ID: "automation-editor", Title: "Automation curves", Description: "Draw vocal rides, filter sweeps and effect movement across the full song."}, surface,
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "automation-overview")), gosx.El("summary", gosx.Text("All saved curves")), continuousAutomation(p)))
}
