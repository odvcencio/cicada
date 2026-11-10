package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/workstation/ui"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/action"
	"m31labs.dev/gosx/command"
	"m31labs.dev/gosx/engine"
	"m31labs.dev/gosx/server"
)

// The typed notes action carries one batch note edit to the Studio service
// (/api/pattern, action "notes"), which writes it as one commit and one Undo
// step. Engines send the batch as the JSON string field "edit"; the Clip tab's
// Quantize form sends op, grid and strength as plain fields.

// noteEditPayload is the "edit" field: the batch plus its version.
type noteEditPayload struct {
	V int `json:"v"`
	edit.EditNotes
}

// noteEditFromForm checks the shape of a batch before it reaches the service:
// version, a known op, at most 512 notes, and integer ranges.
func noteEditFromForm(f map[string]string) (*edit.EditNotes, error) {
	var in edit.EditNotes
	if raw := f["edit"]; raw != "" {
		var payload noteEditPayload
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return nil, fmt.Errorf("the note edit is not valid: %v", err)
		}
		if payload.V != 1 {
			return nil, fmt.Errorf("unsupported note edit version %d", payload.V)
		}
		if payload.Entity != "" || payload.Shared != "" {
			return nil, fmt.Errorf("the note edit names its pattern in the pattern field")
		}
		in = payload.EditNotes
	} else {
		in.Op = f["op"]
		if in.Op != "quantize" {
			return nil, fmt.Errorf("choose a note operation")
		}
		grid, err := integer(f, "grid")
		if err != nil {
			return nil, fmt.Errorf("choose a quantize grid")
		}
		in.Grid = int64(grid)
		if f["strength"] != "" {
			strength, err := integer(f, "strength")
			if err != nil || strength < 1 || strength > 100 {
				return nil, fmt.Errorf("quantize strength must be 1–100%%")
			}
			in.Strength = strength
		}
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return &in, nil
}

func (s *studioApp) notesAction(ctx *action.Context) error {
	if err := actionValues(ctx); err != nil {
		return err
	}
	f := ctx.FormData
	in, err := noteEditFromForm(f)
	if err == nil && f["pattern"] == "" {
		err = fmt.Errorf("choose a pattern")
	}
	if err != nil {
		return action.Validation(err.Error(), nil, f)
	}
	payload := map[string]any{"revision": f["revision"], "source_revision": f["source_revision"], "action": "notes", "pattern": f["pattern"], "notes": in}
	var reply struct {
		Unchanged bool   `json:"unchanged"`
		Message   string `json:"message"`
	}
	if err := s.backend.call(ctx.Request.Context(), http.MethodPost, "/api/pattern", payload, &reply); err != nil {
		var failure *backendError
		if errors.As(err, &failure) {
			result := action.Error(failure.Status, failure.Message)
			result.Result.Values = f
			return result
		}
		return err
	}
	ctx.FormData = nil
	message := ""
	if reply.Unchanged {
		// "Already on the grid." No history entry was written.
		message = reply.Message
	}
	ctx.RedirectBackWithMessage(shellRedirect(ctx.Request, "/"), message)
	return nil
}

type quantizeGrid struct {
	label string
	ticks int64
}

// quantizeGrids are the Quantize targets of a pattern. A performance uses the
// placement snap list without Off; a step pattern or drum lanes offer Beat and
// Bar when they are coarser than its step and a whole number of steps. A grid
// as long as the pattern is left out: it sends every note to the start, which
// the server always refuses.
func quantizeGrids(p project.Pattern) []quantizeGrid {
	if p.Performance != nil {
		var grids []quantizeGrid
		for _, grid := range []quantizeGrid{{"Bar", 3840}, {"Beat", 960}, {"1/8", 480}, {"1/8T", 320}, {"1/16", 240}, {"1/16T", 160}, {"1/32", 120}} {
			if grid.ticks < p.Performance.LengthTicks {
				grids = append(grids, grid)
			}
		}
		return grids
	}
	step := int64(p.StepTicks)
	if step == 0 {
		step = 240
	}
	var grids []quantizeGrid
	for _, grid := range []quantizeGrid{{"Beat", 960}, {"Bar", 3840}} {
		if grid.ticks > step && grid.ticks%step == 0 && grid.ticks < int64(p.Steps)*step {
			grids = append(grids, grid)
		}
	}
	return grids
}

// noteQuantize is the Clip tab's Quantize control. It acts on every note of
// the pattern; Mod+U submits it (notes.quantize) while the Clip tab is on
// screen, which a small engine mounted here reports as $notesVisible.
func (s *studioApp) noteQuantize(ctx *server.Context, v workspace, csrf string, p project.Pattern, returnTo string) gosx.Node {
	grids := quantizeGrids(p)
	// GoSX reuses an engine whose props are unchanged across a navigation, and
	// a reused engine can no longer write signals. The masked CSRF token is
	// fresh for every render, as for the shell engine, so each render remounts.
	props, _ := json.Marshal(map[string]string{"csrf": csrf})
	section := func(children ...gosx.Node) gosx.Node {
		// A group, not a section: editor tests read the Clip panel up to its
		// first closing section tag. The engine mount is an empty sibling of
		// the form, so workspace updates still patch the form itself.
		return gosx.El("div", gosx.Attrs(gosx.Attr("class", "notes-quantize"), gosx.Attr("role", "group"), gosx.Attr("aria-labelledby", "notes-quantize-title"), gosx.Attr("data-gosx-key", "notes-quantize")),
			gosx.El("h4", gosx.Attrs(gosx.Attr("id", "notes-quantize-title")), gosx.Text("Quantize")), gosx.Fragment(children...),
			meterEngine(ctx, engine.Config{Name: "CicadaNotesVisibility", Kind: engine.KindSurface, MountID: "cicada-notes-visibility", Runtime: engine.RuntimeGoWASM, Props: props, RequiredCapabilities: []engine.Capability{engine.CapWASM}}, gosx.El("span", gosx.Attrs(gosx.BoolAttr("hidden")))))
	}
	if len(grids) == 0 {
		return section(gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("No grid is coarser than this pattern's steps and shorter than the pattern, so there is nothing to quantize to.")))
	}
	// The pattern editor's track and scene context survives the redirect.
	query := ctx.Request.URL.Query()
	returnTo = patternContextURL(returnTo, query.Get("track"), query.Get("scene"))
	grid, strength := strconv.FormatInt(grids[0].ticks, 10), "100"
	for _, choice := range grids {
		if p.Performance != nil && choice.ticks == 240 {
			grid = "240"
		}
	}
	state := action.States(ctx.Request)["notes"]
	message := ""
	if !state.OK() && state.Value("pattern") == p.ID {
		message = state.Message()
		if value := state.Value("grid"); value != "" {
			grid = value
		}
		if value := state.Value("strength"); value != "" {
			strength = value
		}
	}
	var options []gosx.Node
	for _, choice := range grids {
		value := strconv.FormatInt(choice.ticks, 10)
		attrs := gosx.Attrs(gosx.Attr("value", value))
		if value == grid {
			attrs = append(attrs, gosx.BoolAttr("selected"))
		}
		options = append(options, gosx.El("option", attrs, gosx.Text(choice.label)))
	}
	children := []gosx.Node{
		hidden("pattern", p.ID), hidden("op", "quantize"), hidden("source_revision", v.SourceRevision),
		field("Grid", gosx.El("select", gosx.Attrs(gosx.Attr("name", "grid")), gosx.Fragment(options...))),
	}
	if p.Performance != nil {
		children = append(children, field("Strength (%)", numberInput("strength", strength, "1", "100")))
	}
	keys := ""
	for _, cmd := range shellCommands() {
		if cmd.ID == "notes.quantize" {
			keys = command.KeyShortcuts(cmd.Keys...)
		}
	}
	children = append(children,
		gosx.El("button", gosx.Attrs(gosx.Attr("type", "submit"), gosx.Attr("aria-keyshortcuts", keys)), gosx.Text("Quantize")),
		// Its own status class: editor suites read the first .form-status in
		// the workspace as the step inspector's status.
		gosx.El("span", gosx.Attrs(gosx.Attr("class", "notes-quantize-status"), gosx.Attr("role", "status"), gosx.Attr("style", "color:var(--coral)")), gosx.Text(message)))
	help := "Moves each note's start to the nearest grid line and keeps its length. One Undo restores the notes."
	if p.Performance == nil {
		help = "Moves each note to the nearest grid line with its hold and velocity. If the grid cannot hold the result, nothing changes and the reason is shown. One Undo restores the notes."
	}
	return section(ui.ActionForm(ui.ActionFormProps{ID: "notes-quantize", Action: "/__actions/notes", CSRF: csrf, Revision: v.Revision, ReturnTo: returnTo}, children...),
		gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text(help)))
}
