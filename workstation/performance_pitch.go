package main

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) performancePitch(ctx *server.Context, v workspace, csrf string, p project.Pattern, selected int) gosx.Node {
	attrs := gosx.Attrs(gosx.Attr("class", "performance-pitch"), gosx.Attr("data-performance-pitch", p.ID), gosx.Attr("data-gosx-key", "performance-pitch-"+p.ID))
	if strings.Contains(p.ID, ".") {
		return gosx.El("section", attrs, gosx.El("h3", gosx.Text("Shape pitch")), gosx.El("p", gosx.Text("Create a local variation to develop these notes while keeping the library intact.")), gosx.El("a", gosx.Attrs(gosx.Attr("href", "#pattern-variation")), gosx.Text("Create a local variation")))
	}
	if len(p.Performance.Notes) == 0 {
		return gosx.El("section", attrs, gosx.El("h3", gosx.Text("Shape pitch")), gosx.El("p", gosx.Text("Add a note before shaping pitch.")))
	}
	pivot := int(p.Performance.Notes[max(0, min(selected, len(p.Performance.Notes)-1))].Note)
	hasCenter := selected >= 0 && selected < len(p.Performance.Notes)
	mirrorLabel := "Select a note to mirror"
	if hasCenter {
		mirrorLabel = "Mirror around " + performancePitch(pivot+int(p.Transpose))
	}
	shift, message := "12", ""
	state := action.States(ctx.Request)["pattern"]
	if !state.OK() && state.Value("pattern") == p.ID && state.Value("action") == "performance-pitch" {
		message = state.Message()
		if state.Value("pitch_operation") == "transpose" {
			shift = state.Value("semitones")
		}
	}
	returnTo := patternContextURL(patternURL(p.ID, 1, "", "")+fmt.Sprintf("&note=%d", selected), ctx.Request.URL.Query().Get("track"), ctx.Request.URL.Query().Get("scene"))
	form := func(operation string, children ...gosx.Node) gosx.Node {
		status := ""
		if state.Value("pitch_operation") == operation {
			status = message
		}
		children = append(children, gosx.El("p", gosx.Attrs(gosx.Attr("class", "form-status"), gosx.Attr("role", "status")), gosx.Text(status)))
		return ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form"}, append([]gosx.Node{hidden("pattern", p.ID), hidden("action", "performance-pitch"), hidden("pitch_operation", operation), hidden("source_revision", v.SourceRevision), hidden("track", ctx.Request.URL.Query().Get("track")), hidden("scene", ctx.Request.URL.Query().Get("scene"))}, children...)...)
	}
	return gosx.El("section", attrs,
		gosx.El("h3", gosx.Text("Shape pitch")),
		gosx.El("p", gosx.Text("Move the whole melody, or turn its rises into falls around the selected note. Timing, velocity and recorded gestures stay the same.")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")),
			form("transpose", field("Shift in semitones", numberInput("semitones", shift, "-48", "48")), submit("", "", "Shift notes")),
			form("reflect", hidden("pivot", strconv.Itoa(pivot)), gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("disabled", !hasCenter)), gosx.Text(mirrorLabel)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changes every use of this pattern. Create a variation first to keep the original; Undo restores the notes. Pitch bends keep their recorded direction.")))
}
