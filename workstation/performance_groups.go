package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) sharedGestures(ctx *server.Context, v workspace, csrf string, p project.Pattern, noteIndex int) gosx.Node {
	groups := p.Performance.Groups
	laneUnit := "lanes"
	if len(groups) == 1 {
		laneUnit = "lane"
	}
	option := func(value, label string, selected bool) gosx.Node {
		return gosx.El("option", gosx.Attrs(gosx.Attr("value", value), gosx.Attr("selected", selected)), gosx.Text(label))
	}
	query := ctx.Request.URL.Query()
	selected, _ := strconv.Atoi(query.Get("group"))
	if !query.Has("group") && noteIndex < len(p.Performance.Notes) && p.Performance.Notes[noteIndex].Group > 0 {
		selected = int(p.Performance.Notes[noteIndex].Group) - 1
	}
	selected = min(max(0, selected), len(groups))
	base := patternContextURL(patternURL(p.ID, 1, "", "")+"&note="+strconv.Itoa(noteIndex), query.Get("track"), query.Get("scene"))
	laneURL := func(i int) string { return base + "&group=" + strconv.Itoa(i) + "#shared-gestures" }
	link := func(i int) string {
		return base + "&group=" + strconv.Itoa(selected) + "&shared_point=" + strconv.Itoa(i) + "#shared-gestures"
	}
	form := func(returnTo, operation string, children ...gosx.Node) gosx.Node {
		children = append(children, performanceFormStatus(ctx, p.ID, operation))
		return ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form gesture-inspector"}, append([]gosx.Node{hidden("pattern", p.ID), hidden("action", operation), hidden("source_revision", v.SourceRevision), hidden("track", query.Get("track")), hidden("scene", query.Get("scene")), hidden("note_index", strconv.Itoa(noteIndex))}, children...)...)
	}
	children := []gosx.Node{
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "gesture-heading")), gosx.El("h3", gosx.Text("Shared gestures")), gosx.El("span", gosx.Text(fmt.Sprintf("%d %s · %d ticks per loop", len(groups), laneUnit, p.Performance.LengthTicks)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Shape several notes with one gesture, including their release tails. Bend adds to each note; pressure uses the larger value; timbre adds around its center. Each note keeps its own vibrato. Shared times stay attached to the loop when notes move.")),
	}
	var navigation []gosx.Node
	for i, group := range groups {
		attrs := gosx.Attrs(gosx.Attr("href", laneURL(i)))
		if i == selected {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		navigation = append(navigation, gosx.El("a", attrs, gosx.Text(group.Name)))
	}
	navigation = append(navigation, gosx.El("a", gosx.Attrs(gosx.Attr("href", laneURL(len(groups)))), gosx.Text("New shared lane")))
	children = append(children, gosx.El("nav", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("aria-label", "Shared gesture lanes")), gosx.Fragment(navigation...)))
	if noteIndex < len(p.Performance.Notes) && len(groups) > 0 {
		note := p.Performance.Notes[noteIndex]
		options := []gosx.Node{option("0", "No shared lane", note.Group == 0)}
		for i, group := range groups {
			options = append(options, option(strconv.Itoa(i+1), group.Name, int(note.Group) == i+1))
		}
		children = append(children, form(laneURL(selected), "performance-binding", field(performancePitch(int(note.Note)+int(p.Transpose))+" · Shared lane", gosx.El("select", gosx.Attrs(gosx.Attr("name", "group")), gosx.Fragment(options...))), submit("", "", "Bind note")))
	}
	name, label := "", "Create shared lane"
	if selected < len(groups) {
		name, label = groups[selected].Name, "Rename shared lane"
	}
	state := action.States(ctx.Request)["pattern"]
	if !state.OK() && state.Value("revision") == v.Revision && state.Value("pattern") == p.ID && state.Value("action") == "performance-group" && state.Value("index") == strconv.Itoa(selected) {
		name = state.Value("name")
	}
	groupFields := []gosx.Node{hidden("index", strconv.Itoa(selected)), field("Lane name", gosx.El("input", gosx.Attrs(gosx.Attr("name", "name"), gosx.Attr("value", name), gosx.Attr("maxlength", "64"), gosx.Attr("pattern", "[a-z_][a-z0-9_\\-]*"), gosx.Attr("required", true)))), submit("intent", "save", label)}
	if selected < len(groups) {
		groupFields = append(groupFields, submit("intent", "remove", "Remove shared lane"))
	}
	children = append(children, form(laneURL(selected), "performance-group", groupFields...))
	if selected == len(groups) {
		return gosx.El("section", gosx.Attrs(gosx.Attr("id", "shared-gestures"), gosx.Attr("class", "performance-gestures")), gosx.Fragment(children...))
	}
	group := groups[selected]
	points := make([]project.PerformedExpression, len(group.Points))
	for i, point := range group.Points {
		points[i] = project.PerformedExpression{Tick: point.Tick, Value: project.NoteExpression{PitchCents: point.PitchCents, Pressure: point.Pressure, Timbre: point.Timbre}}
	}
	pointIndex := gestureSelection(points, query.Get("shared_point"), query.Get("shared_tick"))
	page := min(pointIndex/gesturePageSize, max(0, (len(points)-1)/gesturePageSize))
	start, end := page*gesturePageSize, min(len(points), (page+1)*gesturePageSize)
	var rows []gosx.Node
	for i := start; i < end; i++ {
		point := points[i]
		attrs := gosx.Attrs(gosx.Attr("href", link(i)))
		if i == pointIndex {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		rows = append(rows, gosx.El("tr", gosx.El("th", gosx.Attrs(gosx.Attr("scope", "row")), gosx.El("a", attrs, gosx.Text(fmt.Sprintf("%d ticks", point.Tick)))), gosx.El("td", gosx.Text(fmt.Sprintf("%+g ct", point.Value.PitchCents))), gosx.El("td", gosx.Text(fmt.Sprintf("%g", point.Value.Pressure))), gosx.El("td", gosx.Text(fmt.Sprintf("%g", point.Value.Timbre)))))
	}
	var pages []gosx.Node
	if start > 0 {
		pages = append(pages, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(start-1))), gosx.Text("Previous 64 points")))
	}
	if end < len(points) {
		pages = append(pages, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(end))), gosx.Text("Next 64 points")))
	}
	pages = append(pages, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(len(points)))), gosx.Text("Add a shared point")))
	point := project.PerformedExpression{Value: project.NoteExpression{Timbre: .5}}
	pointLabel := "Add shared point"
	if pointIndex < len(points) {
		point, pointLabel = points[pointIndex], "Save shared point"
	} else if len(points) > 0 {
		point = points[len(points)-1]
		point.Tick = min(p.Performance.LengthTicks-1, point.Tick+1)
	}
	input := func(name string, value float64, low, high string) gosx.Node {
		bits, step := 32, "any"
		if name == "tick" {
			bits, step = 64, "1"
		}
		text := strconv.FormatFloat(value, 'f', -1, bits)
		if !state.OK() && state.Value("revision") == v.Revision && state.Value("pattern") == p.ID && state.Value("action") == "shared-gesture" && state.Value("group_index") == strconv.Itoa(selected) && state.Value("index") == strconv.Itoa(pointIndex) {
			if submitted := state.Value(name); submitted != "" {
				text = submitted
			}
		}
		return gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", name), gosx.Attr("value", text), gosx.Attr("min", low), gosx.Attr("max", high), gosx.Attr("step", step)))
	}
	fields := []gosx.Node{hidden("group_index", strconv.Itoa(selected)), hidden("index", strconv.Itoa(pointIndex)), field("Tick in loop", input("tick", float64(point.Tick), "0", strconv.FormatInt(p.Performance.LengthTicks-1, 10))), field("Shared bend · cents", input("pitch_cents", float64(point.Value.PitchCents), "-9600", "9600")), field("Shared pressure · 0–1", input("pressure", float64(point.Value.Pressure), "0", "1")), field("Shared timbre · 0–1", input("timbre", float64(point.Value.Timbre), "0", "1")), submit("intent", "save", pointLabel)}
	if pointIndex < len(points) && len(points) > 1 {
		fields = append(fields, submit("intent", "remove", "Remove shared point"))
	}
	var heads []gosx.Node
	for _, title := range []string{"Loop tick", "Bend", "Pressure", "Timbre"} {
		heads = append(heads, gosx.El("th", gosx.Attrs(gosx.Attr("scope", "col")), gosx.Text(title)))
	}
	children = append(children,
		gestureTraceLabeled(points, p.Performance.LengthTicks, pointIndex, group.Name+" · shared values in loop time · release tails follow this lane", "Shared bend, pressure and timbre over the pattern loop"),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "gesture-content")), gosx.El("div", gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-table-scroll"), gosx.Attr("role", "region"), gosx.Attr("tabindex", "0"), gosx.Attr("aria-label", "Shared gesture points")), gosx.El("table", gosx.El("caption", gosx.Text(fmt.Sprintf("Points %d–%d of %d", min(start+1, end), end, len(points)))), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...)))), gosx.El("nav", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("aria-label", "Shared gesture pages")), gosx.Fragment(pages...))), form(link(pointIndex), "shared-gesture", fields...)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Each point holds until the next. A new loop starts with neutral values unless a point sets them at tick 0. Saving, binding and removing have Undo. Unbind a lane’s notes before removing it.")))
	return gosx.El("section", gosx.Attrs(gosx.Attr("id", "shared-gestures"), gosx.Attr("class", "performance-gestures")), gosx.Fragment(children...))
}
