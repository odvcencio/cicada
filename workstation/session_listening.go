package main

import (
	"slices"
	"sort"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
)

func (s *studioApp) listeningForm(v workspace, csrf, selected string, children ...gosx.Node) gosx.Node {
	return ui.Form(ui.FormProps{Action: "/__actions/transport", CSRF: csrf, Revision: v.Revision, ReturnTo: placementURL(selected), Class: "action-form"}, children...)
}

func (s *studioApp) sessionListening(v workspace, csrf, selected string, end int64) gosx.Node {
	start, finish := int64(0), min(end, 4*seq.TicksPerBar)
	if v.Project.Arrange != nil {
		for _, place := range v.Project.Arrange.Placements {
			if place.ID == selected {
				start, finish = place.AtTick, place.AtTick+place.LengthTicks
				break
			}
		}
	}
	var section gosx.Node = gosx.Fragment()
	if v.Project.Arrange != nil && len(v.Project.Arrange.Markers) > 0 {
		markers := slices.Clone(v.Project.Arrange.Markers)
		sort.SliceStable(markers, func(i, j int) bool { return markers[i].AtTick < markers[j].AtTick })
		var names []string
		chosen := ""
		for _, marker := range markers {
			if marker.AtTick >= end {
				continue
			}
			names = append(names, marker.ID)
			if chosen == "" || marker.AtTick <= start {
				chosen = marker.ID
			}
		}
		if len(names) > 0 {
			section = s.listeningForm(v, csrf, selected, hidden("action", "loopSection"), field("Section", selectInput("section", chosen, names, "Section")), submit("", "", "Loop section"))
		}
	}
	exact := s.listeningForm(v, csrf, selected, hidden("action", "loop"), field("Loop start", textInput("start", placementLiteral(start, true))), field("Loop end", textInput("end", placementLiteral(finish, true))), submit("", "", "Loop range"))
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "session-listening"), gosx.Attr("aria-label", "Session listening")),
		gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), section,
			s.listeningForm(v, csrf, selected, submit("action", "fullSong", "Play full song")),
			s.listeningForm(v, csrf, selected, submit("action", "stop", "Stop listening"))),
		gosx.El("details", gosx.El("summary", gosx.Text("Loop an exact range")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), exact)),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("A section runs from its marker to the next marker, or the song end. Loop end is exclusive. Playback controls keep your arrangement unchanged.")))
}

func (s *studioApp) placementListening(v workspace, csrf, id string) gosx.Node {
	button := func(mode, loop, label string) gosx.Node {
		return s.listeningForm(v, csrf, id, hidden("action", "audition"), hidden("placement", id), hidden("mode", mode), hidden("loop", loop), submit("", "", label))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions placement-listening"), gosx.Attr("aria-label", "Listen to "+id)),
		button("solo", "false", "Audition solo"), button("context", "false", "Audition in song"), button("solo", "true", "Loop solo"), button("context", "true", "Loop in song"))
}
