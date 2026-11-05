package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"

	"m31labs.dev/cicada/patternmachine"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

func patternMachineLink(track, pattern, scene string) gosx.Node {
	q := url.Values{"panel": {"patterns"}, "machine": {"1"}, "dock": {"clip"}, "region": {"detail"}}
	if track != "" {
		q.Set("track", track)
	}
	if pattern != "" {
		q.Set("pattern", pattern)
	}
	if scene != "" {
		q.Set("scene", scene)
	}
	return gosx.El("a", gosx.Attrs(gosx.Attr("class", "machine-open"), gosx.Attr("href", "/?"+q.Encode()), gosx.Attr("data-machine-open", "")), gosx.Text("Generate pattern"))
}

func machineLanes(p *project.Project, track project.Track) []string {
	if track.Kind == "drums" {
		return []string{"bd", "sd", "ch", "oh", "rs"}
	}
	for _, kit := range p.Kits {
		if kit.ID == track.Kind {
			var lanes []string
			for lane := range kit.Lanes {
				lanes = append(lanes, lane)
			}
			sort.Strings(lanes)
			return lanes
		}
	}
	return nil
}

func machineInt(q url.Values, key string, fallback int) int {
	if q.Get(key) == "" {
		return fallback
	}
	n, err := strconv.Atoi(q.Get(key))
	if err != nil {
		return -1
	}
	return n
}

func machineRow(label string, values []string, active []bool, bar int) gosx.Node {
	cells := []gosx.Node{gosx.El("span", gosx.Attrs(gosx.Attr("class", "machine-lane-name")), gosx.Text(label))}
	for i, value := range values {
		class := "machine-step"
		if active[i] {
			class += " is-active"
		}
		if i%4 == 0 {
			class += " is-beat"
		}
		cells = append(cells, gosx.El("span", gosx.Attrs(gosx.Attr("class", class), gosx.Attr("aria-label", fmt.Sprintf("Bar %d step %d: %s", bar, i+1, value))), gosx.Text(value)))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "machine-lane")), gosx.Fragment(cells...))
}

func machinePreview(p patternmachine.Pattern, kind string, lanes []string, bars int) gosx.Node {
	var barNodes []gosx.Node
	for bar := 0; bar < bars; bar++ {
		rows := []gosx.Node{}
		if kind == "drums" {
			for _, lane := range lanes {
				v := make([]string, 16)
				active := make([]bool, 16)
				for i := range v {
					n := p.Lanes[lane][bar*16+i]
					if n > 0 {
						v[i] = strconv.Itoa(n)
						active[i] = true
					} else {
						v[i] = "·"
					}
				}
				rows = append(rows, machineRow(lane, v, active, bar+1))
			}
		} else {
			v := make([]string, 16)
			active := make([]bool, 16)
			for i := range v {
				n := p.Notes[bar*16+i]
				if n.Velocity > 0 {
					v[i] = patternmachine.Note(n.Pitch)
					active[i] = true
				} else {
					v[i] = "·"
				}
			}
			rows = append(rows, machineRow("Note", v, active, bar+1))
			v = make([]string, 16)
			for i := range v {
				n := p.Notes[bar*16+i]
				if n.Velocity > 0 {
					v[i] = strconv.Itoa(n.Velocity)
				} else {
					v[i] = "·"
				}
			}
			rows = append(rows, machineRow("Velocity", v, active, bar+1))
		}
		barNodes = append(barNodes, gosx.El("section", gosx.Attrs(gosx.Attr("class", "machine-bar"), gosx.Attr("aria-label", fmt.Sprintf("Bar %d", bar+1))), gosx.El("h4", gosx.Text(fmt.Sprintf("BAR %02d", bar+1))), gosx.Fragment(rows...)))
	}
	return gosx.El("div", gosx.Attrs(gosx.Attr("class", "machine-bars"), gosx.Attr("data-machine-preview", "")), gosx.Fragment(barNodes...))
}

func (s *studioApp) patternMachine(ctx *server.Context, v workspace, csrf string) gosx.Node {
	q := ctx.Request.URL.Query()
	var tracks []project.Track
	for _, track := range v.Project.Tracks {
		if patternKindForTrack(v.Project, track) != "" {
			tracks = append(tracks, track)
		}
	}
	if len(tracks) == 0 {
		return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Pattern Machine"}, gosx.El("p", gosx.Text("Add an instrument or drum track first.")))
	}
	selected := tracks[0]
	for _, track := range tracks {
		if track.ID == q.Get("track") {
			selected = track
			break
		}
	}
	kind := patternKindForTrack(v.Project, selected)
	lanes := machineLanes(v.Project, selected)
	seed64, err := strconv.ParseUint(q.Get("seed"), 10, 32)
	if q.Get("seed") == "" {
		seed64 = 34891
		err = nil
	}
	if err == nil && q.Get("variation") == "1" {
		seed64 = uint64(uint32(seed64) + 1)
	}
	recipe := patternmachine.Recipe{Version: 1, Seed: uint32(seed64), Bars: machineInt(q, "bars", 4), Root: int(v.Project.Key.Root), Scale: v.Project.Key.Scale, Density: machineInt(q, "density", 88), Motion: machineInt(q, "motion", 60)}
	var generated patternmachine.Pattern
	if err == nil {
		generated, err = patternmachine.Generate(recipe, kind, lanes)
	} else {
		err = fmt.Errorf("seed must be a whole number from 0 to 4294967295")
	}
	var options []gosx.Node
	for _, t := range tracks {
		options = append(options, gosx.El("option", gosx.Attrs(gosx.Attr("value", t.ID), gosx.Attr("selected", t.ID == selected.ID)), gosx.Text(t.ID)))
	}
	barOptions := []gosx.Node{}
	for _, n := range []int{1, 2, 4} {
		barOptions = append(barOptions, gosx.El("label", gosx.Attrs(gosx.Attr("class", "machine-bar-option")), gosx.El("input", gosx.Attrs(gosx.Attr("type", "radio"), gosx.Attr("name", "bars"), gosx.Attr("value", strconv.Itoa(n)), gosx.Attr("checked", n == recipe.Bars))), gosx.Text(fmt.Sprintf("%d", n))))
	}
	controls := gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "machine-controls"), gosx.Attr("id", "machine-controls")),
		hidden("panel", "patterns"), hidden("machine", "1"), hidden("pattern", q.Get("pattern")), hidden("scene", q.Get("scene")),
		field("Sound", gosx.El("select", gosx.Attrs(gosx.Attr("name", "track")), gosx.Fragment(options...))),
		gosx.El("fieldset", gosx.Attrs(gosx.Attr("class", "machine-length")), gosx.El("legend", gosx.Text("Bars")), gosx.Fragment(barOptions...)),
		field("Density", gosx.El("input", gosx.Attrs(gosx.Attr("type", "range"), gosx.Attr("name", "density"), gosx.Attr("min", "0"), gosx.Attr("max", "100"), gosx.Attr("value", strconv.Itoa(recipe.Density))))),
		field("Motion", gosx.El("input", gosx.Attrs(gosx.Attr("type", "range"), gosx.Attr("name", "motion"), gosx.Attr("min", "0"), gosx.Attr("max", "100"), gosx.Attr("value", strconv.Itoa(recipe.Motion))))),
		gosx.El("details", gosx.El("summary", gosx.Text("Seed")), field("Seed", numberInput("seed", strconv.FormatUint(uint64(recipe.Seed), 10), "0", "4294967295"))),
		submit("", "", "Update preview"))
	back := patternURL(q.Get("pattern"), 1, "", "")
	title := "Orbit arpeggio"
	if kind == "drums" {
		title = "Breakbeat drums"
	}
	header := gosx.El("div", gosx.Attrs(gosx.Attr("class", "machine-header")), gosx.El("div", gosx.El("span", gosx.Attrs(gosx.Attr("class", "machine-eyebrow")), gosx.Text("PATTERN MACHINE")), gosx.El("h3", gosx.Text(title)), gosx.El("p", gosx.Text(fmt.Sprintf("%s · %s %s · %d bars · 1/16", selected.ID, noteNames[recipe.Root], recipe.Scale, recipe.Bars)))), gosx.El("a", gosx.Attrs(gosx.Attr("href", back)), gosx.Text("Back to editor")))
	if err != nil {
		return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Pattern Machine"}, header, controls, gosx.El("p", gosx.Attrs(gosx.Attr("role", "alert")), gosx.Text(err.Error())))
	}
	keepFields := []gosx.Node{hidden("action", "create"), hidden("starter", "machine"), hidden("track", selected.ID), hidden("length", strconv.Itoa(recipe.Bars*16)), hidden("source_revision", v.SourceRevision), hidden("machine_seed", strconv.FormatUint(uint64(recipe.Seed), 10)), hidden("machine_bars", strconv.Itoa(recipe.Bars)), hidden("machine_root", strconv.Itoa(recipe.Root)), hidden("machine_scale", recipe.Scale), hidden("machine_density", strconv.Itoa(recipe.Density)), hidden("machine_motion", strconv.Itoa(recipe.Motion)), field("Pattern name", textInput("newName", availablePatternName(v.Project, selected.ID)))}
	if scene := q.Get("scene"); scene != "" {
		keepFields = append(keepFields, gosx.El("label", gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", "scene"), gosx.Attr("value", scene))), gosx.Text(" Use in scene "+scene)))
	}
	keepFields = append(keepFields, submit("", "", "Keep pattern"))
	keep := ui.Form(ui.FormProps{Action: "/__actions/pattern", CSRF: csrf, Revision: v.Revision, ReturnTo: "/?panel=patterns", Class: "machine-keep"}, keepFields...)
	recipeJSON, _ := json.Marshal(recipe)
	previewName := availablePatternName(v.Project, selected.ID)
	props, _ := json.Marshal(map[string]string{"csrf": csrf, "pattern": previewName, "revision": v.Revision, "sourceRevision": v.SourceRevision, "machine": string(recipeJSON)})
	listen := meterEngine(ctx, engine.Config{Name: "CicadaPatternPreview", Kind: engine.KindSurface, MountID: "cicada-pattern-preview", Runtime: engine.RuntimeGoWASM, Props: props, Capabilities: []engine.Capability{engine.CapFetch, engine.CapKeyboard}, RequiredCapabilities: []engine.Capability{engine.CapWASM, engine.CapFetch}},
		gosx.Fragment(gosx.El("select", gosx.Attrs(gosx.Attr("data-pattern-preview-track", ""), gosx.Attr("aria-label", "Preview sound"), gosx.BoolAttr("hidden")), gosx.El("option", gosx.Attrs(gosx.Attr("value", selected.ID)), gosx.Text(selected.ID))),
			gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-sound-preview", previewName), gosx.Attr("data-sound-name", "generated idea"), gosx.Attr("aria-label", "Loop generated idea"), gosx.Attr("aria-pressed", "false"), gosx.BoolAttr("disabled")), gosx.Text("Loop idea")),
			gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.Attr("data-sound-status", ""), gosx.Attr("role", "status"), gosx.Attr("aria-live", "polite")), gosx.Text("Preparing preview…"))))
	return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Pattern Machine"}, header, gosx.El("p", gosx.Attrs(gosx.Attr("class", "machine-caption")), gosx.Text("A new idea for this sound. Preview the steps, then keep it as an editable pattern.")), controls, machinePreview(generated, kind, lanes, recipe.Bars), gosx.El("div", gosx.Attrs(gosx.Attr("class", "machine-footer")), gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("form", "machine-controls"), gosx.Attr("name", "variation"), gosx.Attr("value", "1"), gosx.Attr("class", "machine-vary")), gosx.Text("New variation")), listen, keep), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Preview leaves your song unchanged. Keeping creates a new editable pattern.")))
}
