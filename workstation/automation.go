package main

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func automationURL(path string) string {
	return "/?" + url.Values{"panel": {"session"}, "automationPath": {path}}.Encode()
}

func automationUnit(unit string) string {
	switch strings.ToLower(unit) {
	case "db":
		return "dB"
	case "hz":
		return "Hz"
	case "ms":
		return "ms"
	default:
		return ""
	}
}

func automationValue(value project.SceneValue) string {
	if value.Number == nil {
		return value.Text
	}
	return strconv.FormatFloat(*value.Number, 'f', -1, 64) + automationUnit(value.Unit)
}

func automationInput(value string, descriptor paramdefs.Descriptor) gosx.Node {
	if descriptor.Curve == "toggle" {
		if value == "1" {
			value = "true"
		} else if value == "0" {
			value = "false"
		}
		return selectInput("value", value, []string{"false", "true"})
	}
	if descriptor.Curve == "enum" {
		return selectInput("value", value, descriptor.Values)
	}
	// Text also accepts catalog alternatives such as off or a synced delay
	// division. The domain validates units and full numeric precision.
	return textInput("value", value)
}

func (s *studioApp) automation(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil || len(v.Project.Scenes) == 0 {
		return gosx.Fragment()
	}
	p := v.Project
	values := map[string]string{}
	descriptors := map[string]paramdefs.Descriptor{}
	var paths, sceneNames []string
	for _, address := range project.ParamAddresses(p) {
		resolved, err := project.ResolveParameterPath(p, address.Address)
		if err != nil || !strings.Contains(address.Address, ".") || !resolved.Descriptor.Live || !resolved.Descriptor.Automatable {
			continue
		}
		paths = append(paths, address.Address)
		descriptors[address.Address] = resolved.Descriptor
		values[address.Address] = fmt.Sprint(address.Value) + automationUnit(resolved.Descriptor.Unit)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return gosx.Fragment()
	}
	selected := paths[0]
	if _, ok := descriptors[ctx.Request.URL.Query().Get("automationPath")]; ok {
		selected = ctx.Request.URL.Query().Get("automationPath")
	}
	form := func(children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/automation", CSRF: csrf, Revision: v.Revision, ReturnTo: automationURL(selected), Class: "action-form"}, children...)
	}
	scenes := map[string]project.Scene{}
	configured := map[string]bool{}
	var editors []gosx.Node
	for _, scene := range p.Scenes {
		sceneNames = append(sceneNames, scene.ID)
		scenes[scene.ID] = scene
		var points []gosx.Node
		for _, point := range scene.Settings {
			configured[point.Path] = true
			value := automationValue(point.Value)
			descriptor, supported := descriptors[point.Path]
			edit := gosx.El("p", gosx.Text(point.Path+" = "+value))
			if supported {
				edit = form(hidden("action", "automation-set"), hidden("scene", scene.ID), hidden("path", point.Path), field(point.Path, automationInput(value, descriptor)), submit("", "", "Save point"))
			}
			remove := form(hidden("action", "automation-remove"), hidden("scene", scene.ID), hidden("path", point.Path), submit("", "", "Remove point"))
			points = append(points, gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("data-automation-path", point.Path)), edit, remove))
		}
		if len(points) > 0 {
			editors = append(editors, gosx.El("section", gosx.Attrs(gosx.Attr("class", "arrangement-block"), gosx.Attr("id", "automation-"+scene.ID)), gosx.El("h3", gosx.Text(scene.ID)), gosx.Fragment(points...)))
		}
	}
	var configuredPaths []string
	for path := range configured {
		configuredPaths = append(configuredPaths, path)
	}
	sort.Strings(configuredPaths)
	columns := []string{"160px"}
	heads := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.Text("PARAMETER"))}
	bar := 1
	for _, entry := range p.Song {
		columns = append(columns, fmt.Sprintf("minmax(110px,%dfr)", entry.Bars))
		heads = append(heads, gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-heading")), gosx.El("small", gosx.Text(fmt.Sprintf("Bar %d · %s", bar, entry.Scene)))))
		bar += int(entry.Bars)
	}
	style := "grid-template-columns:" + strings.Join(columns, " ")
	rows := []gosx.Node{gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("style", style)), gosx.Fragment(heads...))}
	for _, path := range configuredPaths {
		value := values[path]
		if value == "" {
			value = "Score default"
		}
		cells := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.El("strong", gosx.Text(path)))}
		for _, entry := range p.Song {
			point := false
			for _, setting := range scenes[entry.Scene].Settings {
				if setting.Path == path {
					point, value = true, automationValue(setting.Value)
					break
				}
			}
			class, label := "timeline-clip inherited", "↳ "+value
			if point {
				class, label = "timeline-clip", "● "+value
			}
			cells = append(cells, gosx.El("span", gosx.Attrs(gosx.Attr("class", class), gosx.Attr("data-automation-value", value), gosx.Attr("title", entry.Scene+" / "+path)), gosx.Text(label)))
		}
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("style", style), gosx.Attr("data-automation-lane", path)), gosx.Fragment(cells...)))
	}
	chooser := gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "action-form")), hidden("panel", "session"), field("Parameter", selectInput("automationPath", selected, paths)), submit("", "", "Inspect parameter"))
	descriptor := descriptors[selected]
	help := fmt.Sprintf("%s accepts %g to %g %s", selected, descriptor.Min, descriptor.Max, descriptor.Unit)
	if descriptor.Off {
		help += " or off"
	}
	if len(descriptor.Values) > 0 {
		help += "; " + strings.Join(descriptor.Values, ", ")
	}
	point := form(hidden("action", "automation-set"), hidden("path", selected), field("Scene", selectInput("scene", "", sceneNames)), field(selected, automationInput(values[selected], descriptor)), submit("", "", "Add or replace point"))
	lanes := gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll timeline"), gosx.Attr("aria-label", "Automation lanes")), gosx.Fragment(rows...))
	if len(configuredPaths) == 0 {
		lanes = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Add the first point to show its parameter lane across the arrangement."))
	}
	return ui.Panel(ui.PanelProps{ID: "automation", Title: "Scene automation", Description: "Points apply at scene starts with native parameter smoothing; values carry forward until another point changes them."},
		lanes,
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("● sets a value; ↳ carries it. Each point belongs to its scene and applies every time that scene appears. Removing a point keeps the previous value; add the score default explicitly to reset it.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), chooser, gosx.El("p", gosx.Text(help)), point),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks editor-fields")), gosx.Fragment(editors...)))
}
