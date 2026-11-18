package main

import (
	"strconv"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
)

func arrangementTools(p *project.Project, placement bool, barTicks, beatTicks int64, meter string) gosx.Node {
	viewButton := func(action, label string) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-placement-view", action), gosx.BoolAttr("data-studio-view-control")), gosx.Text(label))
	}
	var zoomOptions []gosx.Node
	for _, option := range []struct{ value, label string }{{"fit", "Fit"}, {"48", "1×"}, {"96", "2×"}, {"192", "4×"}, {"384", "8×"}} {
		attrs := gosx.Attrs(gosx.Attr("value", option.value))
		if (placement && option.value == "48") || (!placement && option.value == "fit") {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		zoomOptions = append(zoomOptions, gosx.El("option", attrs, gosx.Text(option.label)))
	}
	var snapOptions []gosx.Node
	barSnap, beatSnap := strconv.FormatInt(barTicks, 10), strconv.FormatInt(beatTicks, 10)
	for _, option := range []struct{ value, label string }{{barSnap, "Bar"}, {beatSnap, "Beat"}, {"480", "1/8"}, {"320", "1/8T"}, {"240", "1/16"}, {"160", "1/16T"}, {"120", "1/32"}, {"1", "Off"}} {
		attrs := gosx.Attrs(gosx.Attr("value", option.value))
		if option.value == "240" {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		label := option.label
		// Studio's grid is still 4/4 (time-signatures phase 5): a bar or beat
		// snap it cannot draw would act as a sixteenth, so it is offered off.
		if (label == "Bar" && option.value != fourFourBar) || (label == "Beat" && option.value != fourFourBeat) {
			attrs = append(attrs, gosx.BoolAttr("disabled"))
			label += " (off until Studio draws " + meter + ")"
		}
		snapOptions = append(snapOptions, gosx.El("option", attrs, gosx.Text(label)))
	}
	class := "arrange-view-tools"
	snap := gosx.El("span", gosx.Attrs(gosx.Attr("class", "scene-snap-hint")), gosx.Text("Snap: whole bars"))
	var placementExtras, editStatus gosx.Node = gosx.Fragment(), gosx.Fragment()
	if placement {
		class = "placement-navigation"
		snap = field("Snap step", gosx.El("select", gosx.Attrs(gosx.Attr("name", "placementSnap"), gosx.BoolAttr("data-studio-view-control"), gosx.Attr("aria-label", "Snap step"), gosx.Attr("title", "Move and trim increments preserve timing offsets. T = triplet. Pattern starts remain on sixteenths. Shift-drag bypasses snap within those limits.")), gosx.Fragment(snapOptions...)))
		placementExtras = gosx.Fragment(gosx.El("label", gosx.Attrs(gosx.Attr("class", "placement-follow")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "placementCopy"), gosx.Attr("aria-label", "Copy on drag"), gosx.BoolAttr("data-studio-view-control"))), gosx.Text("Copy drag")),
			viewButton("selected", "Show selected"))
		editStatus = gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-placement-edit-status"), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite"), gosx.Attr("class", "muted placement-edit-status")), gosx.Text("Drag moves · Option/Alt-drag copies · Shift-drag is free · hold edges to scroll"))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", class), gosx.Attr("aria-label", "Arrangement navigation")),
		arrangementBrush(p, placement),
		// The visible label is short so the strip fits one row; the select's
		// accessible name stays "Timeline zoom".
		field("Zoom", gosx.El("select", gosx.Attrs(gosx.Attr("name", "placementZoom"), gosx.BoolAttr("data-studio-view-control"), gosx.Attr("aria-label", "Timeline zoom")), gosx.Fragment(zoomOptions...))),
		snap,
		viewButton("fit", "Fit song"), placementExtras,
		gosx.El("label", gosx.Attrs(gosx.Attr("class", "placement-follow")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "placementFollow"), gosx.BoolAttr("data-studio-view-control"))), gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-follow-label")), gosx.Text("Follow song"))),
		gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-placement-playback-status"), gosx.Attr("class", "muted")), gosx.Text("Timeline view")),
		editStatus)
}

func arrangementBrush(p *project.Project, placement bool) gosx.Node {
	lengths := make(map[string]int64, len(p.Patterns))
	for _, pattern := range p.Patterns {
		lengths[pattern.ID] = pattern.DurationTicks()
	}
	options := []gosx.Node{gosx.El("option", gosx.Attrs(gosx.Attr("value", "")), gosx.Text("Choose pattern"))}
	for _, track := range p.Tracks {
		var patterns []gosx.Node
		for _, slot := range track.Slots {
			if slot == nil || lengths[*slot] == 0 {
				continue
			}
			attrs := gosx.Attrs(gosx.Attr("value", track.ID+":"+*slot), gosx.Attr("data-length", strconv.FormatInt(lengths[*slot], 10)))
			patterns = append(patterns, gosx.El("option", attrs, gosx.Text(*slot)))
		}
		if len(patterns) != 0 {
			options = append(options, gosx.El("optgroup", gosx.Attrs(gosx.Attr("label", track.ID)), gosx.Fragment(patterns...)))
		}
	}
	hint := "Click a cell on the pattern’s track. The assignment applies wherever that scene plays."
	if placement {
		hint = "Click an empty space on the pattern’s track, or focus the lane and press Enter for its first gap."
	}
	draw := gosx.Attrs(gosx.Attr("value", "draw"))
	if len(options) == 1 {
		draw = append(draw, gosx.BoolAttr("disabled"))
	}
	return gosx.Fragment(
		field("Tool", gosx.El("select", gosx.Attrs(gosx.Attr("name", "arrangeTool"), gosx.Attr("aria-label", "Arrangement tool"), gosx.BoolAttr("data-studio-view-control")),
			gosx.El("option", gosx.Attrs(gosx.Attr("value", "select")), gosx.Text("Select")),
			gosx.El("option", draw, gosx.Text("Draw")))),
		gosx.El("label", gosx.Attrs(gosx.Attr("class", "arrange-brush"), gosx.BoolAttr("hidden")), gosx.Text("Pattern"),
			gosx.El("select", gosx.Attrs(gosx.Attr("name", "arrangeBrush"), gosx.Attr("aria-label", "Pattern to draw"), gosx.BoolAttr("data-studio-view-control")), gosx.Fragment(options...))),
		gosx.El("span", gosx.Attrs(gosx.Attr("class", "arrange-draw-help muted"), gosx.Attr("role", "status"), gosx.Attr("data-draw-hint", hint), gosx.BoolAttr("hidden")), gosx.Text(hint)))
}
