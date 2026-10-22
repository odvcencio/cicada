package main

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) arrangement(ctx *server.Context, v workspace, csrf, selected string) gosx.Node {
	p := v.Project
	if p.Arrange != nil {
		return s.placementArrangement(ctx, v, csrf, selected)
	}
	scenes := map[string]project.Scene{}
	var sceneNames []string
	for _, scene := range p.Scenes {
		scenes[scene.ID] = scene
		sceneNames = append(sceneNames, scene.ID)
	}
	var columns []string
	columns = append(columns, "130px")
	for _, entry := range p.Song {
		columns = append(columns, fmt.Sprintf("minmax(110px,%dfr)", entry.Bars))
	}
	style := "grid-template-columns:" + strings.Join(columns, " ")
	var ruler []gosx.Node
	ruler = append(ruler, gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.Text("ARRANGEMENT")))
	var blocks []gosx.Node
	bar := 1
	for index, entry := range p.Song {
		i := strconv.Itoa(index)
		label := fmt.Sprintf("%s · bars %d–%d", entry.Scene, bar, bar+int(entry.Bars)-1)
		play := s.form(v, csrf, "session", "transport", hidden("entry", i), submit("action", "playFrom", entry.Scene))
		ruler = append(ruler, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-heading")), gosx.El("small", gosx.Text(fmt.Sprintf("%d–%d", bar, bar+int(entry.Bars)-1))), play))
		controls := []gosx.Node{s.form(v, csrf, "session", "song", hidden("action", "scene"), hidden("index", i), field("Scene", selectInput("scene", entry.Scene, sceneNames)), submit("", "", "Replace scene")), s.form(v, csrf, "session", "song", hidden("action", "bars"), hidden("index", i), field("Bars", wholeInput("bars", int(entry.Bars), 1, 999)), submit("", "", "Set length"))}
		controls = append([]gosx.Node{s.listeningForm(v, csrf, selected, hidden("entry", i), submit("action", "loopSection", "Loop block"))}, controls...)
		if index > 0 {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), hidden("target", strconv.Itoa(index-1)), submit("", "", "Move earlier")))
		}
		if index+1 < len(p.Song) {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), hidden("target", strconv.Itoa(index+1)), submit("", "", "Move later")))
		}
		controls = append(controls, s.form(v, csrf, "session", "song", hidden("index", i), submit("action", "duplicate", "Duplicate block")))
		if len(p.Song) > 1 {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("index", i), submit("action", "delete", "Remove block")))
		}
		blocks = append(blocks, gosx.El("details", gosx.Attrs(gosx.Attr("class", "arrangement-block"), gosx.Attr("data-song-index", i)), gosx.El("summary", gosx.Text(label)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.Fragment(controls...))))
		bar += int(entry.Bars)
	}
	var rows []gosx.Node
	rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("style", style)), gosx.Fragment(ruler...)))
	for _, track := range p.Tracks {
		cells := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.El("strong", gosx.Text(track.ID)), gosx.El("small", gosx.Text(track.Kind)))}
		inherited := "off"
		for _, entry := range p.Song {
			binding := scenes[entry.Scene].Bindings[track.ID]
			keep := binding == "" || binding == "keep"
			if !keep {
				inherited = binding
			}
			text := inherited
			class := "timeline-clip"
			if inherited == "off" {
				text, class = "Silence", class+" silent"
			}
			if keep {
				text = "↳ " + text
				class += " inherited"
			}
			attrs := gosx.Attrs(gosx.Attr("class", class), gosx.Attr("title", entry.Scene+" / "+track.ID))
			if inherited != "off" {
				href := patternURL(inherited, 1, "", "")
				if track.Kind == "audio" && p.Edition == 2 {
					href = "/?panel=takes&clip=" + url.QueryEscape(inherited)
				}
				cells = append(cells, gosx.El("a", append(attrs, gosx.Attr("href", href)), gosx.Text(text)))
			} else {
				cells = append(cells, gosx.El("span", attrs, gosx.Text(text)))
			}
		}
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("style", style)), gosx.Fragment(cells...)))
	}
	seconds := project.SongSeconds(p, bar-1)
	length := fmt.Sprintf("%d bars · %d:%02d at %.1f BPM", bar-1, int(seconds)/60, int(seconds)%60, float64(p.TempoMilli)/1000)
	if changes := project.SongTempoChanges(p); len(changes) > 0 {
		length += fmt.Sprintf(" (the tempo changes %d times; the time is the sum of the steps)", len(changes))
	}
	appendBlock := s.form(v, csrf, "session", "song", hidden("action", "append"), field("Scene", selectInput("scene", "", sceneNames)), field("Bars", wholeInput("bars", 4, 1, 999)), submit("", "", "Add to arrangement"))
	settings := s.form(v, csrf, "session", "project", field("Title", textInput("title", p.Title)), field("Tempo (BPM)", numberInput("tempo", strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64), "20", "300")), field("Key", selectInput("root", phraseKeys[p.Key.Root], phraseKeys)), field("Scale", selectInput("scale", p.Key.Scale, phraseScales)), submit("", "", "Save project settings"))
	return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: length}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll timeline"), gosx.Attr("aria-label", "Song timeline")), gosx.Fragment(rows...)), s.sessionListening(v, csrf, selected, int64(bar-1)*seq.TicksPerBar), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Click a scene header to play from that block; click a clip to edit its pattern. ↳ carries the previous pattern.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks")), gosx.Fragment(blocks...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), appendBlock), gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Project settings")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changing the key retunes scale-degree notes. Explicit letter pitches retain their pitch."))))
}

// Named placements are a separate arrangement authority. Showing song controls
// for these projects reports an empty timeline and offers invalid song writes.
func (s *studioApp) placementArrangement(ctx *server.Context, v workspace, csrf, selected string) gosx.Node {
	p := v.Project
	placements := slices.Clone(p.Arrange.Placements)
	sort.SliceStable(placements, func(i, j int) bool { return placements[i].AtTick < placements[j].AtTick })
	if selected == "" && len(placements) > 0 {
		selected = placements[0].ID
	}
	tracks := map[string]string{}
	for _, track := range p.Tracks {
		tracks[track.ID] = track.Kind
	}
	var end int64
	var rows []gosx.Node
	for _, placement := range placements {
		end = max(end, placement.AtTick+placement.LengthTicks)
		href := patternURL(placement.Content, 1, "", "")
		if tracks[placement.Track] == "audio" {
			href = "/?panel=takes&clip=" + url.QueryEscape(placement.Content)
		}
		length := fmt.Sprintf("%d ticks", placement.LengthTicks)
		if placement.LengthTicks%seq.TicksPerBar == 0 {
			length = fmt.Sprintf("%d bars", placement.LengthTicks/seq.TicksPerBar)
		}
		rows = append(rows, gosx.El("tr", gosx.Attrs(gosx.Attr("data-placement-id", placement.ID)),
			gosx.El("th", gosx.Attrs(gosx.Attr("scope", "row")), gosx.Text(placement.ID)),
			gosx.El("td", gosx.Text(placement.Track)),
			gosx.El("td", gosx.El("a", gosx.Attrs(gosx.Attr("href", href)), gosx.Text(placement.Content))),
			gosx.El("td", gosx.Text(placementPosition(placement.AtTick))),
			gosx.El("td", gosx.Text(placementPosition(placement.AtTick+placement.LengthTicks))),
			gosx.El("td", gosx.Text(length))))
	}
	var headers []gosx.Node
	for _, label := range []string{"Placement", "Track", "Pattern or clip", "Start", "End", "Length"} {
		headers = append(headers, gosx.El("th", gosx.Attrs(gosx.Attr("scope", "col")), gosx.Text(label)))
	}
	markers := slices.Clone(p.Arrange.Markers)
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].AtTick < markers[j].AtTick })
	var labels []string
	for _, marker := range markers {
		labels = append(labels, marker.ID+" "+placementPosition(marker.AtTick))
	}
	var markerList gosx.Node
	if len(labels) > 0 {
		markerList = gosx.El("p", gosx.Attrs(gosx.Attr("aria-label", "Arrangement markers")), gosx.El("strong", gosx.Text("Markers: ")), gosx.Text(strings.Join(labels, " · ")))
	}
	seconds := float64(end) * 60000 / (float64(p.TempoMilli) * float64(seq.PPQ))
	if clock, err := project.NewTempoMap(48000, int64(p.TempoMilli), project.SongTempoChanges(p)); err == nil {
		seconds = float64(clock.SampleAtTick(end)) / 48000
	}
	bars := (end + seq.TicksPerBar - 1) / seq.TicksPerBar
	description := fmt.Sprintf("%d bars · %d:%02d at %.1f BPM · %d placements", bars, int(seconds)/60, int(seconds)%60, float64(p.TempoMilli)/1000, len(placements))
	if len(p.Arrange.Tempos) > 0 {
		description += " · tempo changes"
	}
	settings := s.form(v, csrf, "session", "project", field("Title", textInput("title", p.Title)), field("Tempo (BPM)", numberInput("tempo", strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64), "20", "300")), field("Key", selectInput("root", phraseKeys[p.Key.Root], phraseKeys)), field("Scale", selectInput("scale", p.Key.Scale, phraseScales)), submit("", "", "Save project settings"))
	return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: description},
		markerList,
		s.arrangementTempos(v, csrf, selected, end),
		placementTimeline(p, placements, selected, end),
		s.sessionListening(v, csrf, selected, end),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Select a block to edit it. Drag to move; drag its right edge to resize. Arrow keys on a block or edge change it by a sixteenth; hold Shift for a bar. Each placement restarts its pattern; End is exclusive.")),
		s.placementEditors(ctx, v, csrf, placements, selected, end),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "placement-list")), gosx.El("summary", gosx.Text("Placement list")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Named placement timeline")), gosx.El("thead", gosx.El("tr", gosx.Fragment(headers...))), gosx.El("tbody", gosx.Fragment(rows...))))),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Project settings")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings)))
}

func placementPosition(tick int64) string {
	if tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%d ticks", tick)
	}
	return notation.TickPosition(tick)
}
