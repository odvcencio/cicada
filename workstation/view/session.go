// Package view renders Studio views from their supplied state.
package view

import (
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

// SceneLaunchMatrix renders the scene launch controls for a project. The
// caller supplies a non-nil project and the revision and CSRF token to place
// in its forms. Scene and track order follow the score.
func SceneLaunchMatrix(p *project.Project, revision, csrf string) gosx.Node {
	hidden := func(name, value string) gosx.Node {
		return gosx.El("input", gosx.Attrs(gosx.Attr("type", "hidden"), gosx.Attr("name", name), gosx.Attr("value", value)))
	}
	submit := func(value, label string) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", value)), gosx.Text(label))
	}
	form := func(children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/transport", CSRF: csrf, Revision: revision, ReturnTo: "/?panel=session", Class: "action-form"}, children...)
	}
	heads := []gosx.Node{gosx.El("th", gosx.Text("Track"))}
	for _, scene := range p.Scenes {
		heads = append(heads, gosx.El("th", form(hidden("scene", scene.ID), submit("launch", scene.ID))))
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
			cells = append(cells, gosx.El("td", form(hidden("track", track.ID), hidden("pattern", binding), submit(command, label))))
		}
		rows = append(rows, gosx.El("tr", gosx.Fragment(cells...)))
	}
	return ui.Panel(ui.PanelProps{ID: "session", Title: "Scene launch matrix", Description: "Launches use the score timing, or the next bar if none is set."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Scene launch matrix")), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...)))))
}
