package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) performanceEditor(ctx *server.Context, v workspace, csrf string, p project.Pattern, library []gosx.Node) gosx.Node {
	performance := p.Performance
	selected, _ := strconv.Atoi(ctx.Request.URL.Query().Get("note"))
	selected = min(max(0, selected), len(performance.Notes))
	returnTo := patternURL(p.ID, 1, "", "") + "&note=" + strconv.Itoa(selected)
	form := func(children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo, Class: "action-form"}, append([]gosx.Node{hidden("pattern", p.ID)}, children...)...)
	}
	var notes []control.Note
	var rows []gosx.Node
	for index, note := range performance.Notes {
		notes = append(notes, control.Note{Tick: note.Tick, EndTick: note.EndTick, Note: int(note.Note) + int(p.Transpose), Velocity: int(note.Velocity)})
		url := patternURL(p.ID, 1, "", "") + "&note=" + strconv.Itoa(index)
		attrs := gosx.Attrs(gosx.Attr("href", url))
		if selected == index {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		rows = append(rows, gosx.El("tr",
			gosx.El("th", gosx.Attrs(gosx.Attr("scope", "row")), gosx.El("a", attrs, gosx.Text(fmt.Sprintf("%d · %s", index+1, performancePitch(int(note.Note)+int(p.Transpose)))))),
			gosx.El("td", gosx.Text(performancePosition(note.Tick))),
			gosx.El("td", gosx.Text(performanceDuration(note.EndTick-note.Tick))),
			gosx.El("td", gosx.Text(strconv.Itoa(int(note.Velocity))))))
	}
	var heads []gosx.Node
	for _, title := range []string{"Select note", "Starts at", "Held for", "Velocity"} {
		heads = append(heads, gosx.El("th", gosx.Attrs(gosx.Attr("scope", "col")), gosx.Text(title)))
	}
	var tick, end int64
	pitch, velocity, label := 60, 96, "Add note"
	end = min(int64(960), performance.LengthTicks)
	if selected < len(performance.Notes) {
		note := performance.Notes[selected]
		tick, end, pitch, velocity, label = note.Tick, note.EndTick, int(note.Note), int(note.Velocity), "Save note"
	}
	state := action.States(ctx.Request)["pattern"]
	number := func(name string, value, low, high int64) gosx.Node {
		text := strconv.FormatInt(value, 10)
		if !state.OK() && state.Value("revision") == v.Revision && state.Value("pattern") == p.ID &&
			(state.Value("action") == "performed-note" && state.Value("index") == strconv.Itoa(selected) || state.Value("action") == "performance-length" && name == "length_ticks") {
			if submitted := state.Value(name); submitted != "" {
				text = submitted
			}
		}
		return numberInput(name, text, strconv.FormatInt(low, 10), strconv.FormatInt(high, 10))
	}
	children := []gosx.Node{hidden("action", "performed-note"), hidden("index", strconv.Itoa(selected)),
		field("MIDI pitch", number("pitch", int64(pitch), 0, 127)),
		field("Start tick", number("tick", tick, 0, performance.LengthTicks-1)),
		field("End tick", number("end_tick", end, 1, performance.LengthTicks)),
		field("Velocity", number("velocity", int64(velocity), 1, 127)), submit("intent", "save", label)}
	if selected < len(performance.Notes) && len(performance.Notes) > 1 {
		children = append(children, submit("intent", "remove", "Remove note"))
	}
	inspector := gosx.El("section", gosx.Attrs(gosx.Attr("class", "step-inspector performance-inspector")), gosx.El("h3", gosx.Text(label)), form(children...))
	var transpose gosx.Node = gosx.Fragment()
	if p.Transpose != 0 {
		transpose = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(fmt.Sprintf("The chart includes the pattern’s %+d semitone transpose. MIDI pitch edits use the stored pitch before transpose.", p.Transpose)))
	}
	workspace := gosx.El("section", gosx.Attrs(gosx.Attr("class", "pattern-workspace performance-workspace")),
		gosx.El("h3", gosx.Text(p.ID+" · Played timing")),
		s.patternPreview(ctx, v, csrf, p),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "take-summary")), gosx.Text(fmt.Sprintf("%d notes · %s loop", len(notes), performanceDuration(performance.LengthTicks)))),
		notePerformanceTrace(notes, p.ID), transpose,
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "performance-content")),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-table-scroll"), gosx.Attr("tabindex", "0"), gosx.Attr("role", "region"), gosx.Attr("aria-label", "Performed notes")),
				gosx.El("table", gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...))),
				gosx.El("a", gosx.Attrs(gosx.Attr("href", patternURL(p.ID, 1, "", "")+"&note="+strconv.Itoa(len(notes)))), gosx.Text("Add a note"))), inspector),
		s.performanceGestures(ctx, v, csrf, p, selected),
		s.sharedGestures(ctx, v, csrf, p, selected),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("960 ticks per beat. Each note keeps its own gate, velocity and gestures; changes are saved in the score with Undo. Notes of the same pitch need separate tracks when their gates overlap.")),
		gosx.El("details", gosx.El("summary", gosx.Text("Loop length")), form(hidden("action", "performance-length"), field("Length in ticks", number("length_ticks", performance.LengthTicks, 30, 1<<40)), submit("", "", "Save loop length")), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("The loop must include every note’s end. Shortening refuses to discard notes."))))
	return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Pattern editor", Description: "Played timing, independent gates, and a score you can edit."}, s.patternCreation(ctx, v, csrf, ""), gosx.El("div", gosx.Attrs(gosx.Attr("class", "pattern-editor")), gosx.El("nav", gosx.Attrs(gosx.Attr("class", "pattern-library"), gosx.Attr("aria-label", "Pattern library")), gosx.Fragment(library...)), workspace), s.patternManagement(ctx, v, csrf, p))
}
