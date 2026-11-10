package edit

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// AddModulator appends one modulation source: an LFO, a sway, a contour or an
// envelope follower. The completed score is validated and compiled by Apply
// before any file is written. Route it to a parameter with modulationroute.
//
// Example: {"kind":"addmodulator","sourcekind":"lfo","name":"drift","rate":"2bars","wave":"triangle"}
type AddModulator struct {
	SourceKind string `json:"sourcekind" values:"lfo,sway,contour,envelope"`            // the kind of source
	Name       string `json:"name"`                                                     // the source's name
	Rate       string `json:"rate,omitempty"`                                           // the rate or period, such as "2bars", "1/4" or "0.5Hz"; a contour needs a musical period
	Wave       string `json:"wave,omitempty" values:"sine,triangle,square,sample-hold"` // the waveform, for lfo
	Source     string `json:"source,omitempty"`                                         // the track whose level an envelope follows
}

func (AddModulator) Kind() string { return "addmodulator" }

// EditModulator changes one setting or one contour point in its owning file,
// or removes a source that no route uses.
//
// Example: {"kind":"editmodulator","operation":"set","source":"wobble","field":"rate","value":"4bars"}
type EditModulator struct {
	Operation string `json:"operation"`       // set changes a setting; remove deletes the source; the contour operations edit one point
	Source    string `json:"source"`          // the source's name
	Field     string `json:"field,omitempty"` // the setting, for set: wave, rate, phase or loop (lfo); rate, octaves or loop (sway); period or loop (contour); attack, release or source (envelope)
	Value     string `json:"value,omitempty"` // the new value, as source text
	Index     int    `json:"index,omitempty"` // the contour point's index, from 0
	At        string `json:"at,omitempty"`    // the contour point's position, 0 to 1
	Shape     string `json:"shape,omitempty"` // the contour point's shape
}

func (EditModulator) Kind() string { return "editmodulator" }

// ModulationRoute adds, changes the depth of, or removes one identified route.
// Source, target and mode identify a route even when a pair has both modes.
//
// Example: {"kind":"modulationroute","operation":"add","source":"wobble","target":"bass.cutoff","mode":"add","depth":"300Hz"}
type ModulationRoute struct {
	Operation string `json:"operation" values:"add,set,remove"` // add a route, set its depth, or remove it
	Source    string `json:"source"`                            // the modulation source's name
	Target    string `json:"target"`                            // the parameter path, such as bass.cutoff
	Mode      string `json:"mode" values:"add,multiply"`        // how the source combines with the parameter
	Depth     string `json:"depth,omitempty"`                   // the depth with its unit, such as "300Hz" or "2dB", for add and set
}

func (ModulationRoute) Kind() string { return "modulationroute" }

func init() {
	Register("addmodulator", func() Intent { return &AddModulator{} })
	Register("editmodulator", func() Intent { return &EditModulator{} })
	Register("modulationroute", func() Intent { return &ModulationRoute{} })
	Handle("addmodulator", addModulator)
	Handle("editmodulator", editModulator)
	Handle("modulationroute", editModulationRoute)
}

var modName = regexp.MustCompile(`^[a-z_][a-z0-9_-]*(\.[a-z_][a-z0-9_-]*)*$`)

var modSettings = map[string]map[string]bool{
	"lfo":      {"wave": true, "rate": true, "phase": true, "loop": true},
	"sway":     {"rate": true, "octaves": true, "loop": true},
	"contour":  {"period": true, "loop": true},
	"envelope": {"attack": true, "release": true, "source": true},
}

func editModulator(ctx *Context, intent Intent) error {
	in := intent.(*EditModulator)
	if ctx.Options.CaptureGuard != nil {
		if err := ctx.Options.CaptureGuard(); err != nil {
			return err
		}
	}
	if !modName.MatchString(in.Source) {
		return fmt.Errorf("choose a modulation source")
	}
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before editing modulation")
	}
	for _, source := range score.Modulators {
		if source.Name != in.Source {
			continue
		}
		path := source.Position.File
		if path == "" {
			path = ctx.Options.Path
		}
		for _, file := range ctx.Options.Sources {
			if file.Path == path && file.Library != "" {
				return fmt.Errorf("library sources are read-only")
			}
		}
		before, ok := ctx.FileSource(path)
		if !ok {
			return fmt.Errorf("source is outside the editable project")
		}
		offset, err := Offset(before, path, source.Position)
		if err != nil {
			return err
		}
		root, walker, err := notation.ParseTree(before)
		if err != nil {
			return err
		}
		for i := 0; i < root.NamedChildCount(); i++ {
			node := root.NamedChild(i)
			if walker.Type(node) != source.Kind+"_decl" || int(node.StartByte()) != offset {
				continue
			}
			span := Span{Start: int(node.StartByte()), End: int(node.EndByte()), File: path}
			switch in.Operation {
			case "remove":
				for _, route := range score.Routes {
					if route.Source == in.Source {
						return fmt.Errorf("remove this source's routes before removing it")
					}
				}
			case "set":
				if !modSettings[source.Kind][in.Field] || !safeModValue(in.Value) {
					return fmt.Errorf("choose a valid source setting")
				}
				found := false
				for j := 0; j < node.NamedChildCount(); j++ {
					param := node.NamedChild(j)
					if walker.Type(param) == "param_decl" && walker.Text(walker.Field(param, "name")) == in.Field {
						value := walker.Field(param, "value")
						span.Start, span.End, span.Text = int(value.StartByte()), int(value.EndByte()), in.Value
						found = true
						break
					}
				}
				if !found {
					span.Start, span.End, span.Text = int(node.EndByte())-1, int(node.EndByte())-1, " "+in.Field+" = "+in.Value+" "
				}
			case "point-set", "point-add", "point-remove":
				if source.Kind != "contour" {
					return fmt.Errorf("choose a contour source")
				}
				var points []Span
				for j := 0; j < node.NamedChildCount(); j++ {
					point := node.NamedChild(j)
					if walker.Type(point) == "contour_point" {
						points = append(points, Span{Start: int(point.StartByte()), End: int(point.EndByte()), File: path})
					}
				}
				if len(points) != len(source.Points) {
					return fmt.Errorf("contour points could not be located")
				}
				if in.Operation != "point-add" {
					if in.Index < 0 || in.Index >= len(points) {
						return fmt.Errorf("contour point no longer exists")
					}
					span = points[in.Index]
					if strings.Contains(string(before[span.Start:span.End]), "//") || strings.Contains(string(before[span.Start:span.End]), "/*") {
						return fmt.Errorf("edit this commented point in Code")
					}
				}
				if in.Operation != "point-remove" {
					at, ok := notation.ParseContourPosition(in.At)
					level, parseErr := strconv.ParseFloat(in.Value, 64)
					if !ok || parseErr != nil || math.IsNaN(level) || math.IsInf(level, 0) || level < -1 || level > 1 {
						return fmt.Errorf("choose a contour position and value")
					}
					if in.Shape != "" && !notation.ModulatorShapes[in.Shape] {
						return fmt.Errorf("choose a contour shape")
					}
					span.Text = "@" + strings.TrimPrefix(in.At, "@") + " " + strconv.FormatFloat(level, 'g', -1, 64)
					if in.Shape != "" {
						span.Text += " " + in.Shape
					}
					if in.Operation == "point-add" {
						if at.Sign() == 0 || at.Cmp(big.NewRat(1, 1)) == 0 {
							return fmt.Errorf("new points must be between 0 and 1")
						}
						insert := -1
						for j, p := range source.Points {
							old, valid := notation.ParseContourPosition(p.At)
							if valid && old.Cmp(at) == 0 {
								return fmt.Errorf("a point already exists at that position")
							}
							if valid && old.Cmp(at) > 0 {
								insert = j
								break
							}
						}
						if insert < 0 {
							return fmt.Errorf("new points must be between existing endpoints")
						}
						span.Start, span.End = points[insert].Start, points[insert].Start
						span.Text += Newline(before) + "  "
					}
				}
			default:
				return fmt.Errorf("choose a modulation operation")
			}
			after, err := ReplaceSpan(before, path, span)
			if err != nil {
				return err
			}
			ctx.Source, ctx.plan = stageProjectSource(ctx, path, before, after), nil
			ctx.SetLabel("Modulation · " + in.Operation + " " + in.Source)
			return nil
		}
		return fmt.Errorf("source declaration could not be located")
	}
	return fmt.Errorf("modulation source no longer exists")
}

func safeModValue(value string) bool {
	return value != "" && !strings.ContainsAny(value, " \t\r\n{};#=") && !strings.Contains(value, "//") && !strings.Contains(value, "/*")
}

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
