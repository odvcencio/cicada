package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

func (s *studioApp) arrangement(v workspace, csrf string) gosx.Node {
	p := v.Project
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
	seconds := float64(bar-1) * 4 * 60000 / float64(p.TempoMilli)
	length := fmt.Sprintf("%d bars · %d:%02d at %.1f BPM", bar-1, int(seconds)/60, int(seconds)%60, float64(p.TempoMilli)/1000)
	appendBlock := s.form(v, csrf, "session", "song", hidden("action", "append"), field("Scene", selectInput("scene", "", sceneNames)), field("Bars", wholeInput("bars", 4, 1, 999)), submit("", "", "Add to arrangement"))
	settings := s.form(v, csrf, "session", "project", field("Title", textInput("title", p.Title)), field("Tempo (BPM)", numberInput("tempo", strconv.FormatFloat(float64(p.TempoMilli)/1000, 'f', -1, 64), "20", "300")), field("Key", selectInput("root", phraseKeys[p.Key.Root], phraseKeys)), field("Scale", selectInput("scale", p.Key.Scale, phraseScales)), submit("", "", "Save project settings"))
	return ui.Panel(ui.PanelProps{ID: "arrangement", Title: "Arrangement", Description: length}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll timeline"), gosx.Attr("aria-label", "Song timeline")), gosx.Fragment(rows...)), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Click a scene header to play from that block; click a clip to edit its pattern. ↳ carries the previous pattern.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "arrangement-blocks")), gosx.Fragment(blocks...)), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), appendBlock), gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Project settings")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Changing the key retunes scale-degree notes. Explicit letter pitches retain their pitch."))))
}
