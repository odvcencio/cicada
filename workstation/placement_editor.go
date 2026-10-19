package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

func placementURL(id string) string { return "/?panel=session&placement=" + url.QueryEscape(id) }

func (s *studioApp) placementForm(v workspace, csrf, id string, children ...gosx.Node) gosx.Node {
	return ui.Form(ui.FormProps{Action: "/__actions/arrange", CSRF: csrf, Revision: v.Revision, ReturnTo: placementURL(id), Class: "action-form"}, children...)
}

func placementTimeline(p *project.Project, placements []project.Placement, selected string, end int64) gosx.Node {
	for _, marker := range p.Arrange.Markers {
		end = max(end, marker.AtTick+seq.TicksPerBar)
	}
	bars := max(int64(1), (end+seq.TicksPerBar-1)/seq.TicksPerBar)
	ticks := bars * seq.TicksPerBar
	position := func(at int64) string { return strconv.FormatFloat(float64(at)*100/float64(ticks), 'f', 6, 64) + "%" }
	var ruler []gosx.Node
	// Keep the ruler bounded even for long arrangements; its scale remains
	// exact and the horizontally scrolling lanes still show every region.
	stride := max(int64(1), (bars+63)/64)
	for bar := int64(0); bar < bars; bar += stride {
		ruler = append(ruler, gosx.El("span", gosx.Attrs(gosx.Attr("class", "placement-bar"), gosx.Attr("style", "left:"+position(bar*seq.TicksPerBar))), gosx.Text(strconv.FormatInt(bar+1, 10))))
	}
	for _, marker := range p.Arrange.Markers {
		ruler = append(ruler, gosx.El("span", gosx.Attrs(gosx.Attr("class", "placement-marker"), gosx.Attr("style", "left:"+position(marker.AtTick)), gosx.Attr("title", marker.ID+" "+placementPosition(marker.AtTick))), gosx.Text(marker.ID)))
	}
	rows := []gosx.Node{gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-row")), gosx.El("span", gosx.Attrs(gosx.Attr("class", "placement-track")), gosx.Text("BARS")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-ruler")), gosx.Fragment(ruler...)))}
	for _, track := range p.Tracks {
		var blocks []gosx.Node
		var laneEnds []int64
		for _, placement := range placements {
			if placement.Track != track.ID {
				continue
			}
			lane := 0
			for lane < len(laneEnds) && laneEnds[lane] > placement.AtTick {
				lane++
			}
			if lane == len(laneEnds) {
				laneEnds = append(laneEnds, 0)
			}
			laneEnds[lane] = placement.AtTick + placement.LengthTicks
			blocks = append(blocks, gosx.El("div", gosx.Attrs(
				gosx.Attr("class", "placement-block"), gosx.Attr("data-placement-block", placement.ID),
				gosx.Attr("data-placement-at", strconv.FormatInt(placement.AtTick, 10)), gosx.Attr("data-placement-length", strconv.FormatInt(placement.LengthTicks, 10)),
				gosx.Attr("data-placement-selected", strconv.FormatBool(placement.ID == selected)),
				gosx.Attr("style", fmt.Sprintf("left:%s;width:%s;top:%dpx", position(placement.AtTick), position(placement.LengthTicks), 8+lane*68))),
				gosx.El("a", gosx.Attrs(gosx.Attr("class", "placement-select"), gosx.Attr("href", placementURL(placement.ID)), gosx.Attr("draggable", "false"), gosx.Attr("aria-label", "Edit placement "+placement.ID)), gosx.El("strong", gosx.Text(placement.ID)), gosx.El("span", gosx.Text(placement.Content))),
				gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.BoolAttr("data-placement-resize"), gosx.Attr("aria-label", "Resize placement "+placement.ID), gosx.Attr("title", "Drag or use arrow keys to resize")), gosx.El("span", gosx.Attrs(gosx.Attr("aria-hidden", "true")), gosx.Text("⋮")))))
		}
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-row")),
			gosx.El("span", gosx.Attrs(gosx.Attr("class", "placement-track")), gosx.El("strong", gosx.Text(track.ID)), gosx.El("small", gosx.Text(track.Kind))),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-lane"), gosx.Attr("data-placement-ticks", strconv.FormatInt(ticks, 10)), gosx.Attr("style", fmt.Sprintf("height:%dpx", max(1, len(laneEnds))*68+8)), gosx.Attr("role", "group"), gosx.Attr("aria-label", track.ID+" placements")), gosx.Fragment(blocks...))))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll placement-scroll"), gosx.Attr("aria-label", "Placement editor")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-timeline"), gosx.Attr("style", fmt.Sprintf("--placement-bars:%d", bars))), gosx.Fragment(rows...)))
}

func (s *studioApp) placementEditors(v workspace, csrf string, placements []project.Placement, selected string, end int64) gosx.Node {
	p := v.Project
	var tracks []string
	var contents []string
	names := map[string]bool{}
	for _, track := range p.Tracks {
		tracks = append(tracks, track.ID)
	}
	for _, pattern := range p.Patterns {
		contents = append(contents, pattern.ID)
	}
	for _, clip := range p.Clips {
		contents = append(contents, clip.Name)
	}
	for _, placement := range placements {
		names[placement.ID] = true
	}
	for _, marker := range p.Arrange.Markers {
		names[marker.ID] = true
	}
	newName := func(base string) string {
		if len(base) > 55 {
			base = base[:55]
		}
		name := base
		for i := 2; names[name]; i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		return name
	}
	var editors []gosx.Node
	for _, placement := range placements {
		contentURL := patternURL(placement.Content, 1, "", "")
		for _, track := range p.Tracks {
			if track.ID == placement.Track && track.Kind == "audio" {
				contentURL = "/?panel=takes&clip=" + url.QueryEscape(placement.Content)
			}
		}
		copyStart := placement.AtTick + placement.LengthTicks
		for _, other := range placements {
			if other.Track == placement.Track {
				copyStart = max(copyStart, other.AtTick+other.LengthTicks)
			}
		}
		copyStart = (copyStart + seq.TicksPerStep - 1) / seq.TicksPerStep * seq.TicksPerStep
		update := s.placementForm(v, csrf, placement.ID, hidden("action", "update"), hidden("name", placement.ID),
			field("Track", selectInput("track", placement.Track, tracks)), field("Pattern or clip", selectInput("content", placement.Content, contents)),
			field("Start", textInput("at", placementLiteral(placement.AtTick, true))), field("Length", textInput("length", placementLiteral(placement.LengthTicks, false))), submit("", "", "Update placement"))
		duplicate := s.placementForm(v, csrf, placement.ID, hidden("action", "duplicate"), hidden("name", placement.ID),
			field("Copy name", textInput("newName", newName(placement.ID+"-copy"))), field("Copy start", textInput("at", placementLiteral(copyStart, true))),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("data-placement-command", "duplicate")), gosx.Text("Duplicate placement")))
		removeAttrs := gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "delete"), gosx.Attr("data-placement-command", "delete"))
		if len(placements) == 1 {
			removeAttrs = append(removeAttrs, gosx.BoolAttr("disabled"), gosx.Attr("title", "Keep at least one placement in the arrangement"))
		}
		remove := s.placementForm(v, csrf, "", hidden("name", placement.ID), gosx.El("button", removeAttrs, gosx.Text("Remove placement")))
		attrs := gosx.Attrs(gosx.Attr("class", "placement-inspector"), gosx.Attr("data-placement-inspector", placement.ID), gosx.Attr("id", "placement-"+placement.ID))
		if placement.ID == selected {
			attrs = append(attrs, gosx.BoolAttr("open"))
		}
		editors = append(editors, gosx.El("details", attrs, gosx.El("summary", gosx.El("strong", gosx.Text(placement.ID)), gosx.El("span", gosx.Text(fmt.Sprintf("%s · %s → %s", placement.Track, placementPosition(placement.AtTick), placementPosition(placement.AtTick+placement.LengthTicks))))),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), update),
			s.placementListening(v, csrf, placement.ID),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.El("a", gosx.Attrs(gosx.Attr("href", contentURL)), gosx.Text("Edit "+placement.Content)), gosx.El("a", gosx.Attrs(gosx.Attr("href", "/?panel=sampler&placement="+url.QueryEscape(placement.ID))), gosx.Text("Sample into pads")), remove),
			gosx.El("details", gosx.Attrs(gosx.Attr("class", "placement-copy")), gosx.El("summary", gosx.Text("Make a copy")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), duplicate))))
	}
	var markerForms []gosx.Node
	for _, marker := range p.Arrange.Markers {
		markerForms = append(markerForms, s.placementForm(v, csrf, selected, hidden("name", marker.ID), field(marker.ID, textInput("at", placementLiteral(marker.AtTick, true))), submit("action", "marker", "Move marker"), submit("action", "delete-marker", "Remove marker")))
	}
	markerForms = append(markerForms, s.placementForm(v, csrf, selected, hidden("action", "append-marker"), field("Marker name", textInput("newName", newName("new-section"))), field("Marker start", textInput("at", placementLiteral(end, true))), submit("", "", "Add marker")))
	var add gosx.Node
	if len(placements) > 0 {
		first := placements[0]
		start := (end + seq.TicksPerStep - 1) / seq.TicksPerStep * seq.TicksPerStep
		add = s.placementForm(v, csrf, selected, hidden("action", "append"), field("Placement name", textInput("newName", newName("new-placement"))), field("Track", selectInput("track", first.Track, tracks)), field("Pattern or clip", selectInput("content", first.Content, contents)), field("Start", textInput("at", placementLiteral(start, true))), field("Length", textInput("length", "4bars")), submit("", "", "Add placement"))
	}
	return gosx.Fragment(gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-editors")), gosx.Fragment(editors...)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Start uses @bar.beat.step or exact ticks. Length accepts 4bars, 1/8, or 320ticks. Pattern placements on the same track must not overlap.")),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "placement-management")), gosx.El("summary", gosx.Text("Add a placement")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), add)),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "placement-management")), gosx.El("summary", gosx.Text("Edit markers")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), gosx.Fragment(markerForms...))))
}

func placementLiteral(tick int64, position bool) string {
	if position {
		return strings.ReplaceAll(placementPosition(tick), " ticks", "ticks")
	}
	if tick%seq.TicksPerBar == 0 {
		return strconv.FormatInt(tick/seq.TicksPerBar, 10) + "bars"
	}
	return strconv.FormatInt(tick, 10) + "ticks"
}
