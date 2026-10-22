package main

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

const gesturePageSize = 64

func gestureSelection(points []project.PerformedExpression, index, tick string) int {
	selected, _ := strconv.Atoi(index)
	if at, err := strconv.ParseInt(tick, 10, 64); err == nil {
		for i, point := range points {
			if point.Tick == at {
				return i
			}
		}
	}
	return min(max(0, selected), len(points))
}

func (s *studioApp) performanceGestures(ctx *server.Context, v workspace, csrf string, p project.Pattern, noteIndex int) gosx.Node {
	if noteIndex >= len(p.Performance.Notes) {
		return gosx.Fragment()
	}
	note := p.Performance.Notes[noteIndex]
	points, duration := note.Expressions, note.EndTick-note.Tick
	query := ctx.Request.URL.Query()
	selected := gestureSelection(points, query.Get("gesture"), query.Get("gesture_tick"))
	base := patternContextURL(patternURL(p.ID, 1, "", "")+"&note="+strconv.Itoa(noteIndex), query.Get("track"), query.Get("scene"))
	link := func(index int) string { return base + "&gesture=" + strconv.Itoa(index) + "#note-gestures" }
	page := min(selected/gesturePageSize, max(0, (len(points)-1)/gesturePageSize))
	start, end := page*gesturePageSize, min(len(points), (page+1)*gesturePageSize)
	var rows []gosx.Node
	for i := start; i < end; i++ {
		point := points[i]
		attrs := gosx.Attrs(gosx.Attr("href", link(i)))
		if selected == i {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		rows = append(rows, gosx.El("tr", gosx.El("th", gosx.Attrs(gosx.Attr("scope", "row")), gosx.El("a", attrs, gosx.Text(fmt.Sprintf("%d ticks", point.Tick)))),
			gosx.El("td", gosx.Text(fmt.Sprintf("%+g ct", point.Value.PitchCents))),
			gosx.El("td", gosx.Text(fmt.Sprintf("%g", point.Value.Pressure))),
			gosx.El("td", gosx.Text(fmt.Sprintf("%g", point.Value.Timbre)))))
	}
	var navigation []gosx.Node
	if start > 0 {
		navigation = append(navigation, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(start-1))), gosx.Text("Previous 64 points")))
	}
	if end < len(points) {
		navigation = append(navigation, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(end))), gosx.Text("Next 64 points")))
	}
	navigation = append(navigation, gosx.El("a", gosx.Attrs(gosx.Attr("href", link(len(points)))), gosx.Text("Add a gesture point")))
	point := project.PerformedExpression{Value: project.NoteExpression{Timbre: .5}, VibratoRateHz: 5}
	label := "Add gesture point"
	if selected < len(points) {
		point, label = points[selected], "Save gesture point"
	} else if len(points) > 0 {
		point = points[len(points)-1]
		point.Tick = min(duration, point.Tick+1)
	}
	state := action.States(ctx.Request)["pattern"]
	input := func(name string, value float64, low, high string) gosx.Node {
		bits, step := 32, "any"
		if name == "tick" {
			bits, step = 64, "1"
		}
		text := strconv.FormatFloat(value, 'f', -1, bits)
		if !state.OK() && state.Value("revision") == v.Revision && state.Value("pattern") == p.ID && state.Value("action") == "performed-gesture" && state.Value("note_index") == strconv.Itoa(noteIndex) && state.Value("index") == strconv.Itoa(selected) {
			if submitted := state.Value(name); submitted != "" {
				text = submitted
			}
		}
		return gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", name), gosx.Attr("value", text), gosx.Attr("min", low), gosx.Attr("max", high), gosx.Attr("step", step)))
	}
	children := []gosx.Node{hidden("pattern", p.ID), hidden("track", query.Get("track")), hidden("scene", query.Get("scene")), hidden("action", "performed-gesture"), hidden("note_index", strconv.Itoa(noteIndex)), hidden("index", strconv.Itoa(selected)),
		field("Tick after note starts", input("tick", float64(point.Tick), "0", strconv.FormatInt(duration, 10))),
		field("Bend · cents", input("pitch_cents", float64(point.Value.PitchCents), "-9600", "9600")),
		field("Pressure · 0–1", input("pressure", float64(point.Value.Pressure), "0", "1")),
		field("Timbre · 0–1", input("timbre", float64(point.Value.Timbre), "0", "1")),
		field("Vibrato depth · cents", input("vibrato_depth_cents", float64(point.Value.VibratoDepthCents), "0", "9600")),
		field("Vibrato rate · Hz", input("vibrato_rate_hz", float64(point.VibratoRateHz), "0", "100")), submit("intent", "save", label)}
	if selected < len(points) {
		children = append(children, submit("intent", "remove", "Remove gesture point"))
	}
	var heads []gosx.Node
	for _, title := range []string{"Select point", "Bend", "Pressure", "Timbre"} {
		heads = append(heads, gosx.El("th", gosx.Attrs(gosx.Attr("scope", "col")), gosx.Text(title)))
	}
	return gosx.El("section", gosx.Attrs(gosx.Attr("id", "note-gestures"), gosx.Attr("class", "performance-gestures")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "gesture-heading")), gosx.El("h3", gosx.Text(performancePitch(int(note.Note)+int(p.Transpose))+" · Note gestures")), gosx.El("span", gosx.Text(fmt.Sprintf("%d points · %d ticks held", len(points), duration)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Each point holds until the next. Times are relative to this note, so moving the note carries its gestures. Acid and authored synths can play these controls; pressure and timbre need a sound that uses them.")),
		gestureTrace(points, duration, selected),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "gesture-content")),
			gosx.El("div", gosx.El("div", gosx.Attrs(gosx.Attr("class", "take-table-scroll"), gosx.Attr("tabindex", "0"), gosx.Attr("role", "region"), gosx.Attr("aria-label", "Note gesture points")),
				gosx.El("table", gosx.El("caption", gosx.Text(fmt.Sprintf("Points %d–%d of %d", min(start+1, end), end, len(points)))), gosx.El("thead", gosx.El("tr", gosx.Fragment(heads...))), gosx.El("tbody", gosx.Fragment(rows...)))),
				gosx.El("nav", gosx.Attrs(gosx.Attr("class", "actions"), gosx.Attr("aria-label", "Gesture pages")), gosx.Fragment(navigation...))),
			ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: link(selected), Class: "action-form gesture-inspector"}, children...)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("960 ticks per beat. Saving or removing a point has Undo. Shortening a note refuses to discard gesture points. WAV and stems keep the rendered sound; MIDI exports channel-isolated MPE controls with MIDI 1 precision.")))
}

// Bound the overview while retaining each bucket's extrema in all three lanes.
// This affects only the picture; the table, editor and audio use every point.
func gestureOverviewIndices(points []project.PerformedExpression, selected int) []int {
	if len(points) <= 512 {
		indices := make([]int, len(points))
		for i := range indices {
			indices[i] = i
		}
		return indices
	}
	keep := map[int]bool{0: true, len(points) - 1: true}
	if selected < len(points) {
		keep[selected] = true
	}
	values := func(i int) [3]float32 { p := points[i].Value; return [3]float32{p.PitchCents, p.Pressure, p.Timbre} }
	for bucket := 0; bucket < 64; bucket++ {
		start, end := len(points)*bucket/64, len(points)*(bucket+1)/64
		low, high := [3]int{start, start, start}, [3]int{start, start, start}
		for i := start + 1; i < end; i++ {
			v := values(i)
			for lane := 0; lane < 3; lane++ {
				if v[lane] < values(low[lane])[lane] {
					low[lane] = i
				}
				if v[lane] > values(high[lane])[lane] {
					high[lane] = i
				}
			}
		}
		for lane := 0; lane < 3; lane++ {
			keep[low[lane]], keep[high[lane]] = true, true
		}
	}
	indices := make([]int, 0, len(keep))
	for i := range keep {
		indices = append(indices, i)
	}
	slices.Sort(indices)
	return indices
}

func gestureTrace(points []project.PerformedExpression, duration int64, selected int) gosx.Node {
	return gestureTraceLabeled(points, duration, selected, "Held controller values · note starts at 0 ticks", "Bend, pressure and timbre over this note")
}

func gestureTraceLabeled(points []project.PerformedExpression, duration int64, selected int, caption, label string) gosx.Node {
	indices := gestureOverviewIndices(points, selected)
	span := float64(100)
	for _, p := range points {
		span = math.Max(span, math.Abs(float64(p.Value.PitchCents)))
	}
	x := func(tick int64) float64 { return 96 + 888*float64(tick)/float64(duration) }
	var lanes []gosx.Node
	for lane, title := range []string{"Bend · ct", "Pressure", "Timbre"} {
		top := float64(16 + lane*76)
		y := func(value float64) float64 {
			if lane == 0 {
				return top + 28 - 28*value/span
			}
			return top + 56 - 56*value
		}
		value := float64(0)
		if lane == 2 {
			value = .5
		}
		var path strings.Builder
		fmt.Fprintf(&path, "M %.2f %.2f", x(0), y(value))
		for _, i := range indices {
			p := points[i]
			next := []float32{p.Value.PitchCents, p.Value.Pressure, p.Value.Timbre}[lane]
			fmt.Fprintf(&path, " H %.2f V %.2f", x(p.Tick), y(float64(next)))
			value = float64(next)
		}
		fmt.Fprintf(&path, " H %.2f", x(duration))
		lanes = append(lanes, gosx.El("text", gosx.Attrs(gosx.Attr("x", "8"), gosx.Attr("y", fmt.Sprintf("%.0f", top+30))), gosx.Text(title)),
			gosx.El("path", gosx.Attrs(gosx.Attr("class", "gesture-grid"), gosx.Attr("d", fmt.Sprintf("M96 %.0fH984 M96 %.0fH984", top+28, top+56)))),
			gosx.El("path", gosx.Attrs(gosx.Attr("class", fmt.Sprintf("gesture-curve gesture-lane-%d", lane)), gosx.Attr("d", path.String()))))
	}
	if selected < len(points) {
		lanes = append(lanes, gosx.El("path", gosx.Attrs(gosx.Attr("class", "gesture-cursor"), gosx.Attr("d", fmt.Sprintf("M %.2f 8V224", x(points[selected].Tick))))))
	}
	if len(points) > 512 {
		caption += " · overview keeps extrema; use the paged table for every point"
	}
	return gosx.El("figure", gosx.Attrs(gosx.Attr("class", "gesture-trace")), gosx.El("svg", gosx.Attrs(gosx.Attr("viewBox", "0 0 1000 240"), gosx.Attr("role", "img"), gosx.Attr("aria-label", label)), gosx.El("title", gosx.Text(caption)), gosx.Fragment(lanes...)), gosx.El("figcaption", gosx.Text(caption)))
}
