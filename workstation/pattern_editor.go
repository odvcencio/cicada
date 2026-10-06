package main

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/server"
)

func (s *studioApp) patternAction(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	returnTo := ctx.FormData[action.ReturnTargetField]
	destination := returnTo
	err := s.mutation("/api/pattern", func(f map[string]string) (any, error) {
		p := map[string]any{"revision": f["revision"], "action": f["action"], "pattern": f["pattern"]}
		switch f["action"] {
		case "settings":
			swing, err := finiteNumber(f, "swing")
			if err != nil || swing < 50 || swing > 75 || math.Abs(swing*100-math.Round(swing*100)) > 1e-6 {
				return nil, fmt.Errorf("swing must be 50–75%% in increments of 0.01%%")
			}
			gate, err := integer(f, "gate")
			if err != nil {
				return nil, err
			}
			transpose, err := integer(f, "transpose")
			if err != nil {
				return nil, err
			}
			p["settings"] = map[string]int{"swing100": int(math.Round(swing * 100)), "gate": gate, "transpose": transpose}
		case "step", "toggle", "pitch":
			step, err := integer(f, "step")
			if err != nil || step < 1 || step > 64 {
				return nil, fmt.Errorf("step must be 1–64")
			}
			p["step"], p["lane"] = step-1, f["lane"]
			if f["action"] == "step" {
				note := map[string]any{"mode": f["mode"], "velocity": f["velocity"], "accent": f["accent"] == "on", "slide": f["slide"] == "on"}
				for _, key := range []string{"pitch", "ratchet", "chance"} {
					if key == "pitch" && f[key] == "" {
						continue
					}
					value, err := integer(f, key)
					if err != nil {
						return nil, err
					}
					note[key] = value
				}
				p["noteEdit"] = note
			} else if f["action"] == "pitch" {
				pitch, err := integer(f, "pitch")
				if err != nil {
					return nil, err
				}
				p["pitch"] = pitch
			}
			// The inspected step follows a piano-roll or drum-grid edit.
			destination = patternURL(f["pattern"], step, f["lane"], f["octave"])
		case "duplicate":
			p["newName"] = f["newName"]
			destination = patternURL(f["newName"], 1, "", "")
		case "resize":
			length, err := integer(f, "length")
			if err != nil {
				return nil, err
			}
			p["length"] = length
		case "range":
			selection := map[string]any{"operation": f["operation"]}
			for _, key := range []string{"first", "last", "target", "amount"} {
				value, err := integer(f, key)
				if err != nil {
					return nil, err
				}
				if key != "amount" {
					value--
				}
				selection[key] = value
			}
			p["range"], p["lane"] = selection, f["lane"]
		case "bind":
			p["scene"], p["track"] = f["scene"], f["track"]
		default:
			return nil, fmt.Errorf("choose a pattern operation")
		}
		return p, nil
	})(ctx)
	if err == nil && destination != "" {
		// The framework captures native return targets before invoking the
		// handler. Choose the new inspected step explicitly after a success.
		ctx.RedirectWithMessage(destination, "")
	}
	return err
}

func patternURL(id string, step int, lane, octave string) string {
	q := url.Values{"panel": {"patterns"}, "pattern": {id}, "step": {strconv.Itoa(step)}}
	if lane != "" {
		q.Set("lane", lane)
	}
	if octave != "" {
		q.Set("octave", octave)
	}
	return "/?" + q.Encode()
}

func wholeInput(name string, value, low, high int) gosx.Node {
	return gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", name), gosx.Attr("value", strconv.Itoa(value)), gosx.Attr("min", strconv.Itoa(low)), gosx.Attr("max", strconv.Itoa(high)), gosx.Attr("step", "1")))
}

func checkInput(name string, checked bool) gosx.Node {
	return gosx.El("input", gosx.Attrs(gosx.Attr("type", "checkbox"), gosx.Attr("name", name), gosx.Attr("checked", checked)))
}

var noteNames = [...]string{"C", "C♯", "D", "D♯", "E", "F", "F♯", "G", "G♯", "A", "A♯", "B"}

func noteName(pitch int) string { return fmt.Sprintf("%s%d", noteNames[pitch%12], pitch/12-1) }

func (s *studioApp) patterns(ctx *server.Context, v workspace, csrf string) gosx.Node {
	if v.Project == nil || len(v.Project.Patterns) == 0 {
		return failure(fmt.Errorf("save a valid score with a pattern to open the editor"))
	}
	q := ctx.Request.URL.Query()
	p := v.Project.Patterns[0]
	for _, candidate := range v.Project.Patterns {
		if candidate.ID == q.Get("pattern") {
			p = candidate
		}
	}
	step, _ := strconv.Atoi(q.Get("step"))
	step = max(1, min(int(p.Steps), step))
	var library []gosx.Node
	for _, candidate := range v.Project.Patterns {
		attrs := gosx.Attrs(gosx.Attr("href", patternURL(candidate.ID, 1, "", "")), gosx.Attr("class", "pattern-choice"), gosx.Attr("data-gosx-key", "pattern-"+candidate.ID))
		if candidate.ID == p.ID {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		library = append(library, gosx.El("a", attrs, gosx.El("strong", gosx.Text(candidate.ID)), gosx.El("span", gosx.Text(fmt.Sprintf("%s · %d steps", candidate.Kind, candidate.Steps)))))
	}
	var laneNames []string
	usesPhrase := false
	if score, _ := notation.Parse([]byte(v.Source)); score != nil {
		for _, authored := range score.Patterns {
			if authored.Name == p.ID {
				for _, lane := range authored.Lanes {
					laneNames = append(laneNames, lane.Name)
				}
				for _, part := range authored.Parts {
					usesPhrase = usesPhrase || part.Use != nil
				}
			}
		}
	}
	lane := ""
	if len(laneNames) > 0 {
		lane = laneNames[0]
		for _, name := range laneNames {
			if name == q.Get("lane") {
				lane = name
			}
		}
	}
	base, lowest, highest := 48, 128, 0
	for _, note := range p.Data {
		if note != nil && !note.Tie {
			lowest = min(lowest, int(note.Note))
			highest = max(highest, int(note.Note))
		}
	}
	if lowest < 128 {
		base = max(0, min(108, lowest/12*12))
	}
	if octave, err := strconv.Atoi(q.Get("octave")); err == nil && octave >= -1 && octave <= 8 {
		base = (octave + 1) * 12
	}
	octave := strconv.Itoa(base/12 - 1)
	// Start with one octave and grow to the authored notes. An ordinary
	// phrase should be visible immediately instead of sitting below a blank
	// extra octave in the initial piano-roll viewport.
	ceiling := min(127, min(base+47, max(base+11, highest+2)))
	outside := 0
	for _, note := range p.Data {
		if note != nil && !note.Tie && (int(note.Note) < base || int(note.Note) > ceiling) {
			outside++
		}
	}
	form := func(name string, children ...gosx.Node) gosx.Node {
		return ui.Form(ui.FormProps{Action: "/__actions/" + name, CSRF: csrf, Revision: v.Revision, ReturnTo: patternURL(p.ID, step, lane, octave), Class: "action-form"}, append([]gosx.Node{hidden("octave", octave)}, children...)...)
	}
	settings := form("pattern", hidden("action", "settings"), hidden("pattern", p.ID), field("Swing (%)", numberInput("swing", strconv.FormatFloat(float64(p.SwingPercent100)/100, 'f', -1, 64), "50", "75")), field("Gate (%)", wholeInput("gate", int(p.GatePercent), 10, 100)), field("Transpose (semitones)", wholeInput("transpose", int(p.Transpose), -24, 24)), submit("", "", "Save timing"))
	if p.Kind == "drums" {
		settings = form("pattern", hidden("action", "settings"), hidden("pattern", p.ID), hidden("transpose", "0"), field("Swing (%)", numberInput("swing", strconv.FormatFloat(float64(p.SwingPercent100)/100, 'f', -1, 64), "50", "75")), field("Gate (%)", wholeInput("gate", int(p.GatePercent), 10, 100)), submit("", "", "Save timing"))
	}
	var ruler []gosx.Node
	ruler = append(ruler, gosx.El("span", gosx.Attrs(gosx.Attr("class", "roll-label")), gosx.Text("STEP")))
	for i := 1; i <= int(p.Steps); i++ {
		attrs := gosx.Attrs(gosx.Attr("href", patternURL(p.ID, i, lane, octave)), gosx.Attr("class", "step-number"))
		if i == step {
			attrs = append(attrs, gosx.Attr("aria-current", "true"))
		}
		ruler = append(ruler, gosx.El("a", attrs, gosx.Text(strconv.Itoa(i))))
	}
	var rows []gosx.Node
	rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "roll-row ruler"), gosx.Attr("data-gosx-key", "ruler")), gosx.Fragment(ruler...)))
	var viewport gosx.Node = gosx.Fragment()
	if p.Kind == "drums" {
		for _, name := range laneNames {
			buttons := []gosx.Node{hidden("action", "toggle"), hidden("pattern", p.ID), hidden("lane", name), gosx.El("a", gosx.Attrs(gosx.Attr("class", "roll-label"), gosx.Attr("href", patternURL(p.ID, step, name, octave))), gosx.Text(strings.ToUpper(name)))}
			for i := 0; i < int(p.Steps); i++ {
				note := p.Lanes[name][i]
				text := "·"
				if note != nil {
					text = "×"
				}
				buttons = append(buttons, rollButton(i+1, fmt.Sprintf("%s %s step %d", p.ID, name, i+1), text, note, note != nil, i+1 == step && name == lane))
			}
			rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "roll-row drum-row"), gosx.Attr("data-gosx-key", "lane-"+name)), form("pattern", buttons...)))
		}
	} else {
		viewport = gosx.El("form", gosx.Attrs(gosx.Attr("method", "get"), gosx.Attr("action", "/"), gosx.Attr("class", "actions")), hidden("panel", "patterns"), hidden("pattern", p.ID), hidden("step", strconv.Itoa(step)), field("Lowest octave", wholeInput("octave", base/12-1, -1, 8)), submit("", "", "View register"))
		for pitch := ceiling; pitch >= base; pitch-- {
			buttons := []gosx.Node{hidden("action", "pitch"), hidden("pattern", p.ID), hidden("pitch", strconv.Itoa(pitch)), hidden("octave", octave), gosx.El("span", gosx.Attrs(gosx.Attr("class", "roll-label")), gosx.Text(noteName(pitch)))}
			for i := 0; i < int(p.Steps); i++ {
				note := p.Data[i]
				active := note != nil && !note.Tie && int(note.Note) == pitch
				text := ""
				if active {
					text = "●"
				}
				buttons = append(buttons, rollButton(i+1, fmt.Sprintf("%s step %d %s", p.ID, i+1, noteName(pitch)), text, note, active, i+1 == step))
			}
			class := "roll-row"
			if strings.Contains(noteNames[pitch%12], "♯") {
				class += " black-key"
			}
			rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", class), gosx.Attr("data-gosx-key", "pitch-"+strconv.Itoa(pitch))), form("pattern", buttons...)))
		}
		// A dedicated tie row keeps held steps visible even outside the register.
		var ties []gosx.Node
		ties = append(ties, gosx.El("span", gosx.Attrs(gosx.Attr("class", "roll-label")), gosx.Text("HOLD")))
		for i, note := range p.Data {
			text := "·"
			if note != nil && note.Tie {
				text = "—"
			}
			ties = append(ties, gosx.El("a", gosx.Attrs(gosx.Attr("class", "tie-step"), gosx.Attr("href", patternURL(p.ID, i+1, "", octave))), gosx.Text(text)))
		}
		rows = append(rows, gosx.El("div", gosx.Attrs(gosx.Attr("class", "roll-row ruler"), gosx.Attr("data-gosx-key", "ties")), gosx.Fragment(ties...)))
		if outside > 0 {
			viewport = gosx.Fragment(viewport, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(fmt.Sprintf("%d notes are outside this register. View another octave to edit them.", outside))))
		}
	}
	rollTitle := "Piano roll"
	if p.Kind == "drums" {
		rollTitle = "Drum sequencer"
	}
	operations := []string{"copy", "clear", "reverse", "rotate"}
	if p.Kind != "drums" {
		operations = append(operations, "transpose")
	}
	span := min(4, max(1, int(p.Steps)/2))
	rangeEdit := form("pattern", hidden("action", "range"), hidden("pattern", p.ID), hidden("lane", lane), field("Operation", selectInput("operation", "copy", operations)), field("First step", wholeInput("first", 1, 1, int(p.Steps))), field("Last step", wholeInput("last", span, 1, int(p.Steps))), field("Copy to step", wholeInput("target", min(span+1, int(p.Steps)), 1, int(p.Steps))), field("Shift (steps / semitones)", wholeInput("amount", 1, -127, 127)), submit("", "", "Apply to range"))
	resize := form("pattern", hidden("action", "resize"), hidden("pattern", p.ID), field("Pattern length", wholeInput("length", int(p.Steps), 1, 64)), submit("", "", "Resize pattern"))
	tools := gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions"), gosx.BoolAttr("data-workspace-tools"), gosx.BoolAttr("hidden"), gosx.Attr("role", "group"), gosx.Attr("aria-label", "Piano-roll tools")), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-workspace-tool", "select"), gosx.Attr("aria-pressed", "true")), gosx.Text("Select")), gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-workspace-tool", "draw"), gosx.Attr("aria-pressed", "false"), gosx.Attr("aria-keyshortcuts", "B")), gosx.Text("Draw (B)")))
	roll := gosx.El("section", gosx.Attrs(gosx.Attr("class", "pattern-workspace")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "actions")), gosx.El("h3", gosx.Text(p.ID+" · "+rollTitle)), viewport), tools, gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted"), gosx.BoolAttr("data-workspace-help")), gosx.Text("Click a cell to write or clear it. Choose a step number to edit its articulation.")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "pattern-canvas")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "grid-scroll piano-roll"), gosx.Attr("data-pattern-kind", p.Kind), gosx.Attr("data-pattern-id", p.ID)), gosx.Fragment(rows...)), s.stepInspector(ctx, p, step, lane, v.Revision, form)), gosx.El("details", gosx.El("summary", gosx.Text("Pattern timing")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), settings, resize), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Shortening removes steps from the end of every lane. Undo restores them."))), gosx.El("details", gosx.El("summary", gosx.Text("Range editing")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), rangeEdit), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Copy writes the selected steps at the destination. Rotate shifts within the range; transpose changes note pitches. Each operation has one Undo entry."))))
	newName := nextPatternName(v.Project, p.ID)
	if state := action.States(ctx.Request)["pattern"]; !state.OK() && state.Value("action") == "duplicate" && state.Value("pattern") == p.ID {
		newName = state.Value("newName")
	}
	variation := form("pattern", hidden("action", "duplicate"), hidden("pattern", p.ID), field("New pattern name", textInput("newName", newName)), submit("", "", "Create variation"))
	var tracks, scenes []string
	for _, track := range v.Project.Tracks {
		drumTrack := track.Kind == "drums"
		for _, kit := range v.Project.Kits {
			drumTrack = drumTrack || kit.ID == track.Kind
		}
		if track.Kind != "audio" && drumTrack == (p.Kind == "drums") {
			tracks = append(tracks, track.ID)
		}
	}
	for _, scene := range v.Project.Scenes {
		scenes = append(scenes, scene.ID)
	}
	bind := form("pattern", hidden("action", "bind"), hidden("pattern", p.ID), field("Scene", selectInput("scene", "", scenes)), field("Track", selectInput("track", "", tracks)), submit("", "", "Assign to scene"))
	var phraseNotice gosx.Node = gosx.Fragment()
	if usesPhrase {
		phraseNotice = gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("This pattern uses shared phrases. Editing a cell expands its phrase uses locally; the shared phrases and other patterns keep their music."))
	}
	return ui.Panel(ui.PanelProps{ID: "patterns", Title: "Pattern editor", Description: "Reusable patterns, precise notes, and source-backed dynamics."}, gosx.El("div", gosx.Attrs(gosx.Attr("class", "pattern-editor")), gosx.El("nav", gosx.Attrs(gosx.Attr("class", "pattern-library"), gosx.Attr("aria-label", "Pattern library")), gosx.Fragment(library...)), roll), phraseNotice, gosx.El("details", gosx.Attrs(gosx.Attr("class", "pattern-management")), gosx.El("summary", gosx.Text("Variations and scene assignment")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "editor-fields")), variation, bind)))
}

func rollButton(step int, label, text string, note *project.Step, active, selected bool) gosx.Node {
	class := "roll-cell"
	if active && note != nil && note.Accent {
		text = "◆"
	}
	if active && note != nil && note.Slide {
		text = "↗"
	}
	attrs := gosx.Attrs(gosx.Attr("class", class), gosx.Attr("type", "submit"), gosx.Attr("name", "step"), gosx.Attr("value", strconv.Itoa(step)), gosx.Attr("aria-label", label), gosx.Attr("aria-pressed", active), gosx.Attr("data-active", active), gosx.Attr("data-selected", selected))
	if active && note != nil {
		attrs = append(attrs, gosx.Attr("title", fmt.Sprintf("Velocity %d · ratchet %d · chance %d%%", note.Velocity, note.Ratchet, note.Probability)), gosx.Attr("style", fmt.Sprintf("--velocity:%g", float64(note.Velocity)/127)))
	}
	return gosx.El("button", attrs, gosx.Text(text))
}

func (s *studioApp) stepInspector(ctx *server.Context, p project.Pattern, step int, lane, revision string, form func(string, ...gosx.Node) gosx.Node) gosx.Node {
	mode, pitch, velocity := "rest", 60, "x"
	var note *project.Step
	if p.Kind == "drums" && len(p.Lanes[lane]) >= step {
		note = p.Lanes[lane][step-1]
	} else if p.Kind != "drums" && len(p.Data) >= step {
		note = p.Data[step-1]
	}
	ratchet, chance, accent, slide := 1, 100, false, false
	if note != nil {
		mode, pitch, ratchet, chance, accent, slide = "note", int(note.Note), int(note.Ratchet), int(note.Probability), note.Accent, note.Slide
		if note.Tie {
			mode = "tie"
			pitch = 60
			for i := step - 2; i >= 0; i-- {
				if prior := p.Data[i]; prior != nil && !prior.Tie {
					pitch = int(prior.Note)
					break
				}
			}
		}
		if p.Kind == "drums" {
			mode = "hit"
			switch note.Velocity {
			case 127:
				velocity = "X"
			case 100:
			default:
				velocity = strconv.Itoa(int(note.Velocity) / 14)
			}
		}
	}
	values := map[string]string{"mode": mode, "pitch": strconv.Itoa(pitch), "velocity": velocity, "ratchet": strconv.Itoa(ratchet), "chance": strconv.Itoa(chance)}
	if state := action.States(ctx.Request)["pattern"]; !state.OK() && state.Value("revision") == revision && state.Value("action") == "step" && state.Value("pattern") == p.ID && state.Value("step") == strconv.Itoa(step) && state.Value("lane") == lane {
		for key := range values {
			values[key] = state.Value(key)
		}
		accent, slide = state.Value("accent") == "on", state.Value("slide") == "on"
	}
	children := []gosx.Node{hidden("action", "step"), hidden("pattern", p.ID), hidden("step", strconv.Itoa(step)), hidden("lane", lane)}
	if p.Kind == "drums" {
		children = append(children, field("Step type", selectInput("mode", values["mode"], []string{"rest", "hit"})), field("Velocity", selectInput("velocity", values["velocity"], []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "x", "X"})))
	} else {
		children = append(children, field("Step type", selectInput("mode", values["mode"], []string{"rest", "note", "tie"})), field("MIDI pitch", numberInput("pitch", values["pitch"], "0", "127")), field("Accent", checkInput("accent", accent)), field("Slide", checkInput("slide", slide)))
	}
	children = append(children, field("Ratchet", numberInput("ratchet", values["ratchet"], "1", "8")), field("Chance (%)", numberInput("chance", values["chance"], "1", "100")), submit("", "", "Save step"))
	title := fmt.Sprintf("Step %d", step)
	if lane != "" {
		title += " · " + strings.ToUpper(lane)
	}
	return gosx.El("section", gosx.Attrs(gosx.Attr("class", "step-inspector"), gosx.Attr("id", "step-inspector")), gosx.El("h3", gosx.Text(title)), form("pattern", children...))
}

func nextPatternName(p *project.Project, id string) string {
	if len(id) > 52 {
		id = id[:52]
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", id, n)
		found := false
		for _, pattern := range p.Patterns {
			found = found || pattern.ID == candidate
		}
		if !found {
			return candidate
		}
	}
}
