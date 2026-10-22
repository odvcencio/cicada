package main

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) performanceTime(ctx *server.Context, v workspace, csrf string, p project.Pattern, selected int) gosx.Node {
	if strings.Contains(p.ID, ".") {
		var uses []gosx.Node
		if v.Project.Arrange != nil {
			for _, place := range v.Project.Arrange.Placements {
				if place.Content == p.ID {
					uses = append(uses, gosx.El("a", gosx.Attrs(gosx.Attr("href", placementURL(place.ID))), gosx.Text("Open "+place.ID+" to make a local variation")))
				}
			}
		}
		return gosx.El("section", gosx.Attrs(gosx.Attr("class", "performance-time"), gosx.Attr("data-performance-time", p.ID)), gosx.El("h3", gosx.Text("Shape time")), gosx.El("p", gosx.Text("Library patterns stay unchanged. Make an editable local variation before shaping time.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.Fragment(uses...)))
	}
	var buttons []gosx.Node
	for _, choice := range []struct {
		numerator, denominator int64
		label                  string
	}{{1, 2, "Half as long"}, {2, 3, "Two thirds as long"}, {3, 2, "One and a half as long"}, {2, 1, "Twice as long"}} {
		length := (p.Performance.LengthTicks*choice.numerator + choice.denominator/2) / choice.denominator
		buttons = append(buttons, gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "scale"), gosx.Attr("value", fmt.Sprintf("%d/%d", choice.numerator, choice.denominator)), gosx.Attr("disabled", length < 30 || length > 1<<40)), gosx.Text(choice.label+" · "+patternBeatLength(length))))
	}
	message := ""
	state := action.States(ctx.Request)["pattern"]
	if !state.OK() && state.Value("pattern") == p.ID && state.Value("action") == "performance-scale" {
		message = state.Message()
	}
	returnTo := patternURL(p.ID, 1, "", "") + fmt.Sprintf("&note=%d", selected)
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "performance-time"), gosx.Attr("data-performance-time", p.ID), gosx.Attr("data-gosx-key", "performance-time-"+p.ID)),
		gosx.El("h3", gosx.Text("Shape time")),
		gosx.El("p", gosx.Text("Move the notes, gates and gestures together. Pitch and project tempo stay the same.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form"},
			hidden("pattern", p.ID), hidden("action", "performance-scale"), hidden("source_revision", v.SourceRevision), gosx.Fragment(buttons...), gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(message)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changes every use of this pattern. Existing song blocks keep their length. Make a variation first to develop one use; Undo restores the original timing.")))
}
