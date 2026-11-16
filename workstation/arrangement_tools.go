package main

import (
	"strconv"

	"m31labs.dev/gosx"
)

func arrangementTools(placement bool, barTicks, beatTicks int64, meter string) gosx.Node {
	viewButton := func(action, label string) gosx.Node {
		return gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-placement-view", action), gosx.BoolAttr("data-studio-view-control")), gosx.Text(label))
	}
	var zoomOptions []gosx.Node
	for _, option := range []struct{ value, label string }{{"fit", "Fit song"}, {"48", "1×"}, {"96", "2×"}, {"192", "4×"}, {"384", "8×"}} {
		attrs := gosx.Attrs(gosx.Attr("value", option.value))
		if option.value == "48" {
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
		if (label == "Bar" && option.value != fourFourBar) || (label == "Beat" && option.value != fourFourBeat) {
			attrs = append(attrs, gosx.BoolAttr("disabled"))
			label += " (off until Studio draws " + meter + ")"
		}
		snapOptions = append(snapOptions, gosx.El("option", attrs, gosx.Text(label)))
	}
	class := "arrange-view-tools"
	snap := gosx.El("span", gosx.Text("Snap: whole bars"))
	var placementExtras, editStatus gosx.Node = gosx.Fragment(), gosx.Fragment()
	if placement {
		class = "placement-navigation"
		snap = field("Snap step", gosx.El("select", gosx.Attrs(gosx.Attr("name", "placementSnap"), gosx.BoolAttr("data-studio-view-control"), gosx.Attr("aria-label", "Snap step"), gosx.Attr("title", "Move and trim increments preserve timing offsets. T = triplet. Pattern starts remain on sixteenths. Shift-drag bypasses snap within those limits.")), gosx.Fragment(snapOptions...)))
		placementExtras = gosx.Fragment(gosx.El("label", gosx.Attrs(gosx.Attr("class", "placement-follow")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "placementCopy"), gosx.Attr("aria-label", "Copy on drag"), gosx.BoolAttr("data-studio-view-control"))), gosx.Text("Copy drag")),
			viewButton("selected", "Show selected"))
		editStatus = gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-placement-edit-status"), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite"), gosx.Attr("class", "muted placement-edit-status")), gosx.Text("Drag moves · Option/Alt-drag copies · Shift-drag is free · hold edges to scroll"))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", class), gosx.Attr("aria-label", "Arrangement navigation")),
		field("Timeline zoom", gosx.El("select", gosx.Attrs(gosx.Attr("name", "placementZoom"), gosx.BoolAttr("data-studio-view-control"), gosx.Attr("aria-label", "Timeline zoom")), gosx.Fragment(zoomOptions...))),
		snap,
		viewButton("fit", "Fit song"), placementExtras,
		gosx.El("label", gosx.Attrs(gosx.Attr("class", "placement-follow")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "placementFollow"), gosx.Attr("aria-label", "Follow song"), gosx.BoolAttr("data-studio-view-control"))), gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-follow-label")), gosx.Text("Follow song"))),
		gosx.El("span", gosx.Attrs(gosx.BoolAttr("data-placement-playback-status"), gosx.Attr("class", "muted")), gosx.Text("Timeline view")),
		editStatus)
}
