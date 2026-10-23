package main

import (
	"fmt"
	"strconv"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/controller"
	"m31labs.dev/gosx/server"
)

// The workbench disclosure shares its state with canonical page updates, so
// changing the step count cannot close the editor during a musical gesture.
func registerArrangementTempos(ctx *server.Context) {
	ctx.Runtime().Controller(controller.Config{
		Name: "arrangement-tempos", Root: "#arrangement",
		Outputs: []controller.Output{{Name: "open", Signal: "$tempoStepsOpen", Initial: false}},
		Storage: &controller.Storage{Area: "session", Namespace: "studio:tempo", Load: []controller.StorageSlot{{Key: "open", Signal: "$tempoStepsOpen"}}, Save: []controller.StorageSlot{{Key: "open", Signal: "$tempoStepsOpen"}}},
	})
}

func (s *studioApp) arrangementTempos(v workspace, csrf, selected string, end int64) gosx.Node {
	p := v.Project
	lastBar := int(min(int64(1<<20+1), max(int64(1), (end+seq.TicksPerBar-1)/seq.TicksPerBar)))
	bpm := func(milli int) string { return strconv.FormatFloat(float64(milli)/1000, 'f', -1, 64) }
	used := map[int]bool{}
	var rows []gosx.Node
	for _, step := range p.Arrange.Tempos {
		bar := step.Bar + 1
		used[bar] = true
		at := fmt.Sprintf("@%d.1.1", bar)
		form := s.placementForm(v, csrf, selected, hidden("oldat", at),
			field("Bar", wholeInput("bar", bar, 1, lastBar)),
			field("Tempo (BPM)", numberInput("bpm", bpm(step.BPMMilli), "20", "300")),
			submit("action", "set-tempo", "Save tempo step"),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("name", "action"), gosx.Attr("value", "remove-tempo"), gosx.BoolAttr("formnovalidate")), gosx.Text("Remove step")))
		rows = append(rows, gosx.El("li", gosx.Attrs(gosx.Attr("data-tempo-step", at), gosx.Attr("data-gosx-key", "tempo-step-"+at)),
			gosx.El("strong", gosx.Text(at+" · "+bpm(step.BPMMilli)+" BPM")), form))
	}
	nextBar := 1
	for used[nextBar] && nextBar <= lastBar {
		nextBar++
	}
	var add gosx.Node
	if nextBar <= lastBar {
		tempo := p.TempoMilli
		for _, step := range p.Arrange.Tempos {
			if step.Bar < nextBar {
				tempo = step.BPMMilli
			}
		}
		add = gosx.El("div", gosx.Attrs(gosx.Attr("data-tempo-add", "")), s.placementForm(v, csrf, selected,
			field("Bar", wholeInput("bar", nextBar, 1, lastBar)),
			field("Tempo (BPM)", numberInput("bpm", bpm(tempo), "20", "300")),
			submit("action", "set-tempo", "Add tempo step")))
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("id", "arrangement-tempos"), gosx.Attr("class", "gsx-collapsible tempo-steps editor-fields"), gosx.Attr("data-gosx-collapsible", "$tempoStepsOpen"), gosx.Attr("data-arrangement-tempos", "")),
		gosx.El("summary", gosx.Text(fmt.Sprintf("Tempo steps · %d", len(rows)))),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Each step sets the tempo from the start of its bar. Change a bar to move the step; removing it carries the previous tempo forward.")),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Base tempo: "+bpm(p.TempoMilli)+" BPM. Recorded audio keeps its original speed.")),
		gosx.El("ol", gosx.Fragment(rows...)), add)
}
