package main

import (
	"crypto/sha256"
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
	return s.arrangementView(ctx, v, csrf, selected, false)
}

func (s *studioApp) arrangementView(ctx *server.Context, v workspace, csrf, selected string, shell bool) gosx.Node {
	p := v.Project
	if p.Arrange != nil {
		return s.placementArrangement(ctx, v, csrf, selected)
	}
	openPattern := ""
	openTrack := ""
	selectedBlock := -1
	if ctx != nil {
		query := ctx.Request.URL.Query()
		openPattern = query.Get("pattern")
		openTrack = query.Get("track")
		index, err := strconv.Atoi(query.Get("songBlock"))
		if err == nil && index >= 0 && index < len(p.Song) && query.Get("songKey") == songBlockKey(p.Song, index) {
			selectedBlock = index
		}
	}
	scenes := map[string]project.Scene{}
	var sceneNames []string
	for _, scene := range p.Scenes {
		scenes[scene.ID] = scene
		sceneNames = append(sceneNames, scene.ID)
	}
	var columns []string
	columns = append(columns, "var(--timeline-track-width,130px)")
	tickStart := int64(0)
	for _, entry := range p.Song {
		length := p.MeterMap().BarsTicks(tickStart, int64(entry.Bars))
		columns = append(columns, fmt.Sprintf("minmax(0,%.9ffr)", float64(length)))
		tickStart += length
	}
	style := "grid-template-columns:" + strings.Join(columns, " ") + ";--timeline-scenes:" + strconv.Itoa(len(p.Song))
	var ruler []gosx.Node
	ruler = append(ruler, gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.Text("Tracks")))
	var blocks []gosx.Node
	generationParts := []string{v.Filename}
	for index := range p.Song {
		generationParts = append(generationParts, songBlockKey(p.Song, index))
	}
	generationHash := sha256.Sum256([]byte(strings.Join(generationParts, "\n")))
	generation := fmt.Sprintf("%x", generationHash[:6])
	bar := 1
	for index, entry := range p.Song {
		i, block := strconv.Itoa(index), targetField(songBlockKey(p.Song, index))
		label := fmt.Sprintf("%s · bars %d–%d", entry.Scene, bar, bar+int(entry.Bars)-1)
		play := s.form(v, csrf, "session", "transport", hidden("entry", i), submit("action", "playFrom", entry.Scene))
		selection := url.Values{"panel": {"session"}, "songBlock": {i}, "songKey": {songBlockKey(p.Song, index)}}
		selectionPath := "/"
		if ctx != nil && ctx.Request.URL.Path == "/studio" {
			selection = ctx.Request.URL.Query()
			selection.Del("panel")
			selection.Del("reveal")
			selection.Del("songRevision")
			selection.Set("songBlock", i)
			selection.Set("songKey", songBlockKey(p.Song, index))
			selectionPath = "/studio"
		}
		editLabel := "Edit " + label
		editLink := gosx.El("a", gosx.Attrs(gosx.Attr("href", selectionPath+"?"+selection.Encode()+"#song-block-"+i), gosx.Attr("class", "timeline-edit"), gosx.Attr("data-song-index", i), gosx.Attr("aria-label", editLabel)), gosx.Text("Edit"))
		ruler = append(ruler, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-heading"), gosx.Attr("data-scene-start", strconv.FormatInt(p.MeterMap().BarStart(int64(bar-1)), 10)), gosx.Attr("data-scene-length", strconv.FormatInt(p.MeterMap().BarsTicks(p.MeterMap().BarStart(int64(bar-1)), int64(entry.Bars)), 10)), gosx.Attr("title", fmt.Sprintf("Bars %d–%d", bar, bar+int(entry.Bars)-1))),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-section")), gosx.El("small", gosx.Text(fmt.Sprintf("%02d", index+1))), play, editLink),
			barRuler(bar, int(entry.Bars))))
		controls := []gosx.Node{s.form(v, csrf, "session", "song", hidden("action", "scene"), hidden("index", i), block, field("Scene", selectInput("scene", entry.Scene, sceneNames)), submit("", "", "Replace scene")), s.form(v, csrf, "session", "song", hidden("action", "bars"), hidden("index", i), block, field("Bars", wholeInput("bars", int(entry.Bars), 1, 999)), submit("", "", "Set length"))}
		controls = append([]gosx.Node{s.listeningForm(v, csrf, selected, hidden("entry", i), submit("action", "loopSection", "Loop block"))}, controls...)
		if index > 0 {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), block, hidden("target", strconv.Itoa(index-1)), submit("", "", "Move earlier")))
		}
		if index+1 < len(p.Song) {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("action", "move"), hidden("index", i), block, hidden("target", strconv.Itoa(index+1)), submit("", "", "Move later")))
		}
		controls = append(controls, s.form(v, csrf, "session", "song", hidden("index", i), block, submit("action", "duplicate", "Duplicate block")))
		if len(p.Song) > 1 {
			controls = append(controls, s.form(v, csrf, "session", "song", hidden("index", i), block, submit("action", "delete", "Remove block")))
		}
		attrs := gosx.Attrs(gosx.Attr("class", "arrangement-block"), gosx.Attr("data-song-index", i), gosx.Attr("data-song-key", songBlockKey(p.Song, index)), gosx.Attr("data-song-generation", generation), gosx.Attr("id", "song-block-"+i))
		if index == selectedBlock {
			attrs = append(attrs, gosx.BoolAttr("open"), gosx.BoolAttr("data-song-selected"))
		}
		blockNode := gosx.El("details", attrs, gosx.El("summary", gosx.Text(label)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.Fragment(controls...)))
		if index == selectedBlock {
			blocks = append([]gosx.Node{blockNode}, blocks...)
		} else {
			blocks = append(blocks, blockNode)
		}
		bar += int(entry.Bars)
	}
	rail := s.trackRail(ctx, v, csrf)
	var rows []gosx.Node
	rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("style", style)), gosx.Fragment(ruler...)))
	for trackIndex, track := range p.Tracks {
		cells := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "timeline-track")), gosx.El("strong", gosx.Attrs(gosx.Attr("title", track.ID)), gosx.Text(track.ID)), gosx.El("small", gosx.Attrs(gosx.Attr("title", track.Kind)), gosx.Text(track.Kind)), rail(track.ID))}
		silentScenes := silentSongScenes(p, track.ID)
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
			content := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "clip-name")), gosx.Text(text))}
			if inherited != "off" {
				href := patternURL(inherited, 1, "", "")
				if track.Kind == "audio" && p.Edition == 2 {
					href = "/?panel=takes&clip=" + url.QueryEscape(inherited)
				} else {
					scene := ""
					if !keep {
						scene = entry.Scene
					}
					href = patternContextURL(href, track.ID, scene) + "&dock=clip&region=detail"
					attrs = append(attrs, gosx.BoolAttr("data-scene-pattern-open"))
					content = append(content, clipPreview(p, inherited, p.MeterMap().BarsTicks(0, int64(entry.Bars))))
					if inherited == openPattern && (openTrack == "" || openTrack == track.ID) {
						attrs = append(attrs, gosx.BoolAttr("data-arrange-pattern-open"))
					}
				}
				cells = append(cells, gosx.El("a", append(attrs, gosx.Attr("href", href)), gosx.Fragment(content...)))
			} else if silentScenes[entry.Scene] {
				q := url.Values{"panel": {"patterns"}, "createScene": {"1"}, "track": {track.ID}, "scene": {entry.Scene}, "dock": {"clip"}, "region": {"detail"}}
				attrs = append(attrs, gosx.BoolAttr("data-scene-pattern-open"), gosx.Attr("data-scene-create", entry.Scene), gosx.Attr("aria-label", "Silent. Create a pattern for "+track.ID+" in scene "+entry.Scene), gosx.Attr("href", "/?"+q.Encode()+"#patterns"))
				cells = append(cells, gosx.El("a", attrs, gosx.El("span", gosx.Attrs(gosx.Attr("class", "clip-name")), gosx.Text("+ Create pattern"))))
			} else {
				cells = append(cells, gosx.El("span", attrs, gosx.Fragment(content...)))
			}
		}
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-row"), gosx.Attr("data-arrange-kind", track.Kind), gosx.Attr("data-track-color", strconv.Itoa(trackIndex%trackColors)), gosx.Attr("style", style)), gosx.Fragment(cells...)))
	}
	seconds := project.SongSeconds(p, bar-1)
	length := fmt.Sprintf("Scene matrix · %d bars · %d:%02d at %.1f BPM", bar-1, int(seconds)/60, int(seconds)%60, float64(p.TempoMilli)/1000)
	if changes := project.SongTempoChanges(p); len(changes) > 0 {
		length += fmt.Sprintf(" (the tempo changes %d times; the time is the sum of the steps)", len(changes))
	}
	var selectionNotice gosx.Node = gosx.Fragment()
	if ctx != nil && ctx.Request.URL.Query().Get("songBlock") != "" && selectedBlock < 0 {
		selectionNotice = gosx.El("p", gosx.Attrs(gosx.Attr("role", "status")), gosx.Text("The song changed. Choose Edit on a block to continue editing it."))
	}
	appendBlock := s.form(v, csrf, "session", "song", hidden("action", "append"), field("Scene", selectInput("scene", "", sceneNames)), field("Bars", wholeInput("bars", 4, 1, 999)), submit("", "", "Add to arrangement"))
	settings := s.form(v, csrf, "session", "project", field("Title", textInput("title", p.Title)), field("Tempo (BPM)", numberInput("tempo", strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64), "20", "300")), field("Key", selectInput("root", phraseKeys[p.Key.Root], phraseKeys)), field("Scale", selectInput("scale", p.Key.Scale, phraseScales)), submit("", "", "Save project settings"))
	if shell {
		panel := func(label string, content ...gosx.Node) gosx.Node {
			return gosx.El("details", gosx.Attrs(gosx.Attr("class", "arrange-popover"), gosx.Attr("name", "arrange-tools"), gosx.BoolAttr("data-discard-on-close"), gosx.BoolAttr("data-dismiss-outside")), gosx.El("summary", gosx.Text(label)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrange-popover-panel")), gosx.Fragment(content...)))
		}
		end := p.MeterMap().BarStart(int64(bar - 1))
		exact := s.listeningForm(v, csrf, selected, hidden("action", "loop"), field("Loop start", textInput("start", placementLiteral(p.MeterMap(), 0, true))), field("Loop end", textInput("end", placementLiteral(p.MeterMap(), min(end, p.MeterMap().BarStart(4)), true))), submit("", "", "Loop range"))
		tools := gosx.El("section", gosx.Attrs(gosx.Attr("class", "arrange-tools"), gosx.Attr("aria-label", "Arrangement tools"), gosx.BoolAttr("data-dismiss-scope")),
			s.listeningForm(v, csrf, selected, submit("action", "fullSong", "Play full song")),
			s.listeningForm(v, csrf, selected, submit("action", "stop", "Stop listening")),
			arrangementTools(false, 0, 0, ""),
			panel("Loop an exact range", exact, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("A section runs from its marker to the next marker, or the song end. Loop end is exclusive. Playback controls keep your arrangement unchanged."))),
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrange-tools-spacer"))),
			panel("Add section", appendBlock),
			panel("Project", settings, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changing the key retunes scale-degree notes. Explicit letter pitches retain their pitch."))))
		return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement"}, gosx.El("div", gosx.Attrs(gosx.BoolAttr("data-scene-navigation"), gosx.Attr("data-placement-bars", strconv.Itoa(bar-1))), tools,
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll timeline"), gosx.Attr("aria-label", "Song timeline"), gosx.Attr("data-scene-ticks", strconv.FormatInt(tickStart, 10))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "scene-timeline"), gosx.Attr("style", fmt.Sprintf("--placement-bars:%d", bar-1))), gosx.El("div", gosx.Attrs(gosx.Attr("class", "placement-playhead"), gosx.BoolAttr("data-placement-playhead"), gosx.BoolAttr("hidden"), gosx.Attr("aria-hidden", "true"))), gosx.Fragment(rows...)))),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "arrange-status")), gosx.Text(length+" · Click a clip to edit it below. ↳ carries the previous pattern. After a song edit, choose Edit again to continue.")),
			gosx.El("p", gosx.Attrs(gosx.BoolAttr("data-scene-open-status"), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite"), gosx.Attr("class", "scene-open-status"))), selectionNotice,
			gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks"), gosx.Attr("role", "group"), gosx.Attr("aria-label", "Section inspector")), gosx.Fragment(blocks...)))
	}
	return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: length}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll timeline"), gosx.Attr("aria-label", "Song timeline")), gosx.Fragment(rows...)), gosx.El("p", gosx.Attrs(gosx.BoolAttr("data-scene-open-status"), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite"), gosx.Attr("class", "scene-open-status"))), s.sessionListening(v, csrf, selected, p.MeterMap().BarStart(int64(bar-1))), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Click a scene name to play from that block, or Edit to change the block. After a song edit, choose Edit again to continue. Click a clip to edit its pattern. ↳ carries the previous pattern.")), selectionNotice, gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks")), gosx.Fragment(blocks...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), appendBlock), gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Project settings")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changing the key retunes scale-degree notes. Explicit letter pitches retain their pitch."))))
}

// silentSongScenes contains only scenes whose every song occurrence is
// silent for this track. A keep binding can be silent in one occurrence and
// carry a sounding pattern in another, so one cell alone is not sufficient.
func silentSongScenes(p *project.Project, trackID string) map[string]bool {
	if p == nil || p.Arrange != nil {
		return nil
	}
	eligible := false
	chained := false
	for _, track := range p.Tracks {
		if track.ID == trackID && patternKindForTrack(p, track) != "" {
			eligible = true
			chained = len(track.Chain) > 0
			break
		}
	}
	if !eligible {
		return nil
	}
	scenes := make(map[string]project.Scene, len(p.Scenes))
	for _, scene := range p.Scenes {
		scenes[scene.ID] = scene
	}
	silent := make(map[string]bool)
	// A chain plays from the song start until a scene binds the track.
	playing := "off"
	if chained {
		playing = "chain"
	}
	for _, entry := range p.Song {
		scene, ok := scenes[entry.Scene]
		if !ok {
			return nil
		}
		if binding := scene.Bindings[trackID]; binding != "" && binding != "keep" {
			playing = binding
		}
		if _, seen := silent[entry.Scene]; !seen {
			silent[entry.Scene] = true
		}
		if playing != "off" {
			silent[entry.Scene] = false
		}
	}
	return silent
}

// trackColors is the size of the Arrange track palette; tracks take its colors
// in order, so neighbors differ.
const trackColors = 6

// barRuler numbers the bars of one song block. Long blocks label every few
// bars so the numbers stay readable.
func barRuler(first, bars int) gosx.Node {
	every := max(1, (bars+15)/16)
	var labels []gosx.Node
	for offset := 0; offset < bars; offset += every {
		labels = append(labels, gosx.El("span", gosx.Attrs(gosx.Attr("style", "grid-column:span "+strconv.Itoa(min(every, bars-offset)))), gosx.Text(strconv.Itoa(first+offset))))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "timeline-bars"), gosx.Attr("aria-hidden", "true"), gosx.Attr("style", "grid-template-columns:repeat("+strconv.Itoa(bars)+",minmax(0,1fr))")), gosx.Fragment(labels...))
}

// Named placements are a separate arrangement authority. Showing song controls
// for these projects reports an empty timeline and offers invalid song writes.
func (s *studioApp) placementArrangement(ctx *server.Context, v workspace, csrf, selected string) gosx.Node {
	p := v.Project
	meterMap := p.MeterMap()
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
		if bars, whole := meterMap.WholeBars(placement.AtTick, placement.LengthTicks); whole {
			length = fmt.Sprintf("%d bars", bars)
		}
		rows = append(rows, gosx.El("tr", gosx.Attrs(gosx.Attr("data-placement-id", placement.ID)),
			gosx.El("th", gosx.Attrs(gosx.Attr("scope", "row")), gosx.Text(placement.ID)),
			gosx.El("td", gosx.Text(placement.Track)),
			gosx.El("td", gosx.El("a", gosx.Attrs(gosx.Attr("href", href)), gosx.Text(placement.Content))),
			gosx.El("td", gosx.Text(placementPosition(v.Project.MeterMap(), placement.AtTick))),
			gosx.El("td", gosx.Text(placementPosition(v.Project.MeterMap(), placement.AtTick+placement.LengthTicks))),
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
		labels = append(labels, marker.ID+" "+placementPosition(v.Project.MeterMap(), marker.AtTick))
	}
	var markerList gosx.Node
	if len(labels) > 0 {
		markerList = gosx.El("p", gosx.Attrs(gosx.Attr("aria-label", "Arrangement markers")), gosx.El("strong", gosx.Text("Markers: ")), gosx.Text(strings.Join(labels, " · ")))
	}
	seconds := float64(end) * 60000 / (float64(p.TempoMilli) * float64(seq.PPQ))
	if clock, err := project.NewTempoMap(meterMap, 48000, int64(p.TempoMilli), project.SongTempoChanges(p)); err == nil {
		seconds = float64(clock.SampleAtTick(end)) / 48000
	}
	bars := meterMap.BarsUntil(end)
	description := fmt.Sprintf("Placement lanes · %d bars · %d:%02d at %.1f BPM · %d placements", bars, int(seconds)/60, int(seconds)%60, float64(p.TempoMilli)/1000, len(placements))
	if len(p.Arrange.Tempos) > 0 {
		description += " · tempo changes"
	}
	settings := s.form(v, csrf, "session", "project", field("Title", textInput("title", p.Title)), field("Tempo (BPM)", numberInput("tempo", strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64), "20", "300")), field("Key", selectInput("root", phraseKeys[p.Key.Root], phraseKeys)), field("Scale", selectInput("scale", p.Key.Scale, phraseScales)), submit("", "", "Save project settings"))
	return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: description},
		placementTimeline(p, placements, selected, end, s.trackRail(ctx, v, csrf)),
		markerList,
		s.arrangementTempos(v, csrf, selected, end),
		s.sessionListening(v, csrf, selected, end),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Select a block to edit it. Drag to move; drag its right edge to resize. Arrow keys on a block or edge change it by a sixteenth; hold Shift for a bar. Each placement restarts its pattern; End is exclusive.")),
		s.placementEditors(ctx, v, csrf, placements, selected, end),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "placement-list")), gosx.El("summary", gosx.Text("Placement list")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll")), gosx.El("table", gosx.Attrs(gosx.Attr("aria-label", "Named placement timeline")), gosx.El("thead", gosx.El("tr", gosx.Fragment(headers...))), gosx.El("tbody", gosx.Fragment(rows...))))),
		gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Project settings")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings)))
}

func placementPosition(meterMap seq.MeterMap, tick int64) string {
	if tick%seq.TicksPerStep != 0 {
		return fmt.Sprintf("%d ticks", tick)
	}
	return notation.TickPosition(meterMap, tick)
}
