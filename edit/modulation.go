package edit

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// AddModulator appends one source. The completed score is validated and
// compiled by Apply before any file is written.
type AddModulator struct {
	SourceKind string `json:"sourcekind"`
	Name       string `json:"name"`
	Rate       string `json:"rate,omitempty"`
	Wave       string `json:"wave,omitempty"`
	Source     string `json:"source,omitempty"`
}

func (AddModulator) Kind() string { return "addmodulator" }

// ModulationRoute adds, changes the depth of, or removes one identified route.
// Source, target and mode identify a route even when a pair has both modes.
type ModulationRoute struct {
	Operation string `json:"operation"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Mode      string `json:"mode"`
	Depth     string `json:"depth,omitempty"`
}

func (ModulationRoute) Kind() string { return "modulationroute" }

func init() {
	Register("addmodulator", func() Intent { return &AddModulator{} })
	Register("modulationroute", func() Intent { return &ModulationRoute{} })
	Handle("addmodulator", addModulator)
	Handle("modulationroute", editModulationRoute)
}

var modName = regexp.MustCompile(`^[a-z_][a-z0-9_-]*(\.[a-z_][a-z0-9_-]*)*$`)

func addModulator(ctx *Context, intent Intent) error {
	in := intent.(*AddModulator)
	if ctx.Options.CaptureGuard != nil {
		if err := ctx.Options.CaptureGuard(); err != nil {
			return err
		}
	}
	if !modName.MatchString(in.Name) || strings.Contains(in.Name, ".") {
		return fmt.Errorf("choose a source name")
	}
	var text string
	switch in.SourceKind {
	case "lfo":
		if _, err := notation.ParseModRate(in.Rate); err != "" || (in.Wave != "sine" && in.Wave != "triangle" && in.Wave != "square" && in.Wave != "sample-hold") {
			return fmt.Errorf("choose a waveform and rate")
		}
		text = fmt.Sprintf("lfo %s { wave = %s rate = %s }", in.Name, in.Wave, in.Rate)
	case "sway":
		if _, err := notation.ParseModRate(in.Rate); err != "" {
			return fmt.Errorf("choose a rate")
		}
		text = fmt.Sprintf("sway %s { rate = %s }", in.Name, in.Rate)
	case "contour":
		if rate, err := notation.ParseModRate(in.Rate); err != "" || rate.Free {
			return fmt.Errorf("choose a period")
		}
		text = fmt.Sprintf("contour %s { period = %s @0 0 @1 0 }", in.Name, in.Rate)
	case "envelope":
		if !modName.MatchString(in.Source) {
			return fmt.Errorf("choose a source track")
		}
		text = fmt.Sprintf("envelope %s { source = %s }", in.Name, in.Source)
	default:
		return fmt.Errorf("choose lfo, sway, contour or envelope")
	}
	decls, err := textDeclarations([]byte(text))
	if err != nil || len(decls) != 1 || decls[0].kind != in.SourceKind+"_decl" || decls[0].name != in.Name {
		return fmt.Errorf("invalid source declaration")
	}
	nl := Newline(ctx.Source)
	ctx.Source = append(append([]byte(nil), ctx.Source...), []byte(nl+text+nl)...)
	ctx.plan = nil
	ctx.SetLabel("Modulation · added " + in.Name)
	return nil
}

func editModulationRoute(ctx *Context, intent Intent) error {
	in := intent.(*ModulationRoute)
	if ctx.Options.CaptureGuard != nil {
		if err := ctx.Options.CaptureGuard(); err != nil {
			return err
		}
	}
	if !modName.MatchString(in.Source) || !modName.MatchString(in.Target) || (in.Mode != "add" && in.Mode != "multiply") {
		return fmt.Errorf("choose a source, target and route mode")
	}
	if in.Operation != "remove" {
		value, _, err := paramdefs.ParseBase(in.Depth)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("enter a finite route depth")
		}
	}
	if in.Operation == "add" {
		text := fmt.Sprintf("mod %s -> %s depth = %s %s", in.Source, in.Target, in.Depth, in.Mode)
		decls, err := textDeclarations([]byte(text))
		if err != nil || len(decls) != 1 || decls[0].kind != "mod_decl" {
			return fmt.Errorf("invalid route")
		}
		nl := Newline(ctx.Source)
		ctx.Source = append(append([]byte(nil), ctx.Source...), []byte(nl+text+nl)...)
		ctx.plan = nil
		ctx.SetLabel("Modulation · added route")
		return nil
	}
	if in.Operation != "set" && in.Operation != "remove" {
		return fmt.Errorf("choose a route operation")
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before editing routes")
	}
	for _, route := range score.Routes {
		mode := route.Mode
		if mode == "" {
			mode = "add"
		}
		if route.Source != in.Source || route.Target != in.Target || mode != in.Mode {
			continue
		}
		path := route.Position.File
		if path == "" {
			path = ctx.Options.Path
		}
		for _, file := range ctx.Options.Sources {
			if file.Path == path && file.Library != "" {
				return fmt.Errorf("library routes are read-only")
			}
		}
		before, ok := ctx.FileSource(path)
		if !ok {
			return fmt.Errorf("route source is outside the editable project")
		}
		offset, err := Offset(before, path, route.Position)
		if err != nil {
			return err
		}
		root, walker, err := notation.ParseTree(before)
		if err != nil {
			return err
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			node := root.NamedChild(i)
			if walker.Type(node) != "mod_decl" || int(node.StartByte()) != offset {
				continue
			}
			span := Span{Start: int(node.StartByte()), End: int(node.EndByte()), File: path}
			if in.Operation == "set" {
				found := false
				for j := 0; j < node.NamedChildCount(); j++ {
					param := node.NamedChild(j)
					if walker.Type(param) == "param_decl" && walker.Text(walker.Field(param, "name")) == "depth" {
						value := walker.Field(param, "value")
						span.Start, span.End, span.Text = int(value.StartByte()), int(value.EndByte()), in.Depth
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("route depth is missing")
				}
			}
			after, err := ReplaceSpan(before, path, span)
			if err != nil {
				return err
			}
			ctx.Source, ctx.plan = stageProjectSource(ctx, path, before, after), nil
			ctx.SetLabel("Modulation · " + in.Operation + " route")
			return nil
		}
		return fmt.Errorf("route declaration could not be located")
	}
	return fmt.Errorf("route no longer exists")
}
