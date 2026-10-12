package edit

import (
	"bytes"
	"fmt"
	"strconv"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/taproot/walk"
	"m31labs.dev/cicada/notation"
)

// MoveClip moves a placement to a new start tick. Only the authored `at`
// literal changes.
type MoveClip struct {
	Entity EntityID `json:"entity"`
	AtTick int64    `json:"attick"`
}

func (MoveClip) Kind() string { return "moveclip" }

// ResizeClip sets a placement length. The right edge moves; `at` is untouched.
type ResizeClip struct {
	Entity      EntityID `json:"entity"`
	LengthTicks int64    `json:"lengthticks"`
}

func (ResizeClip) Kind() string { return "resizeclip" }

// SplitClip cuts a placement in two at a pattern-length boundary.
type SplitClip struct {
	Entity EntityID `json:"entity"`
	AtTick int64    `json:"attick"`
}

func (SplitClip) Kind() string { return "splitclip" }

// DuplicateClip places a copy of a placement directly after the original.
type DuplicateClip struct {
	Entity EntityID `json:"entity"`
}

func (DuplicateClip) Kind() string { return "duplicateclip" }

// DeleteClip removes a placement and the comment lines attached above it.
type DeleteClip struct {
	Entity EntityID `json:"entity"`
}

func (DeleteClip) Kind() string { return "deleteclip" }

func init() {
	Register("moveclip", func() Intent { return &MoveClip{} })
	Register("resizeclip", func() Intent { return &ResizeClip{} })
	Register("splitclip", func() Intent { return &SplitClip{} })
	Register("duplicateclip", func() Intent { return &DuplicateClip{} })
	Register("deleteclip", func() Intent { return &DeleteClip{} })
	for _, kind := range []string{"moveclip", "resizeclip", "splitclip", "duplicateclip", "deleteclip"} {
		Handle(kind, editClipPlacement)
	}
}

// resolvePlacement resolves a placement entity against the working source.
func resolvePlacement(ctx *Context, id EntityID) (Placement, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return Placement{}, err
	}
	if id.Kind() != KindPlacement {
		return Placement{}, fmt.Errorf("clip edits need a placement entity, got %q", id)
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		// A score that does not compile can still name its placements. Editors
		// repair such scores (an off-grid start), so read them from the syntax.
		if score, _, parseErr := ctx.ParseProject(); parseErr == nil && score != nil && score.Arrange != nil {
			for _, p := range score.Arrange.Placements {
				if p.Name == id.Name() {
					return Placement{ID: p.Name, Track: p.Track, Content: p.Content, AtTick: p.AtTick, LengthTicks: p.LengthTicks}, nil
				}
			}
		}
		return Placement{}, err
	}
	entity, err := Resolve(plan, id)
	if err != nil {
		return Placement{}, err
	}
	return plan.Placements[entity.Index], nil
}

func editClipPlacement(ctx *Context, intent Intent) error {
	var updated []byte
	var label string
	var err error
	switch in := intent.(type) {
	case *MoveClip:
		var placement Placement
		if placement, err = resolvePlacement(ctx, in.Entity); err != nil {
			return err
		}
		position := FormatPosition(in.AtTick)
		if updated, err = notation.SetPlacementField(ctx.Source, placement.ID, "at", position); err != nil {
			return err
		}
		label = fmt.Sprintf("Clip %s moved to %s", placement.ID, position)
	case *ResizeClip:
		var placement Placement
		if placement, err = resolvePlacement(ctx, in.Entity); err != nil {
			return err
		}
		length := FormatLength(in.LengthTicks)
		if updated, err = notation.SetPlacementField(ctx.Source, placement.ID, "length", length); err != nil {
			return err
		}
		label = fmt.Sprintf("Clip %s resized to %s", placement.ID, length)
	case *SplitClip:
		updated, label, err = splitClip(ctx, in)
	case *DuplicateClip:
		updated, label, err = duplicateClip(ctx, in)
	case *DeleteClip:
		updated, label, err = deleteClip(ctx, in)
	}
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel(label)
	return nil
}

// placementDecl finds the place_decl node for name in the arrange block.
func placementDecl(source []byte, name string) (decl, arrange *gts.Node, walker *walk.Walker, err error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		a := root.NamedChild(i)
		if walker.Type(a) != "arrange_decl" {
			continue
		}
		for j := 0; j < a.NamedChildCount(); j++ {
			p := a.NamedChild(j)
			if walker.Type(p) == "place_decl" && walker.Text(walker.Field(p, "name")) == name {
				return p, a, walker, nil
			}
		}
	}
	return nil, nil, nil, fmt.Errorf("placement %q is not in the arrange block", name)
}

// placementCopy returns the declaration text of decl with a new name and time
// literals, keeping the original layout.
func placementCopy(source []byte, decl *gts.Node, walker *walk.Walker, name, at, length string) (string, error) {
	start := int(decl.StartByte())
	var spans []Span
	spans = append(spans, Span{int(walker.Field(decl, "name").StartByte()) - start, int(walker.Field(decl, "name").EndByte()) - start, name, ""})
	found := map[string]bool{}
	for k := 0; k < decl.NamedChildCount(); k++ {
		param := decl.NamedChild(k)
		if walker.Type(param) != "param_decl" {
			continue
		}
		field := walker.Text(walker.Field(param, "name"))
		value := walker.Field(param, "value")
		switch field {
		case "at":
			spans = append(spans, Span{int(value.StartByte()) - start, int(value.EndByte()) - start, at, ""})
			found[field] = true
		case "length":
			spans = append(spans, Span{int(value.StartByte()) - start, int(value.EndByte()) - start, length, ""})
			found[field] = true
		}
	}
	if !found["at"] || !found["length"] {
		return "", fmt.Errorf("placement needs at and length fields")
	}
	text, err := PatchSpans(source[start:decl.EndByte()], "", spans)
	return string(text), err
}

// freePlacementName returns base-2, base-3, ... that no declaration uses.
func freePlacementName(plan *Plan, source []byte, base string) string {
	for n := 2; ; n++ {
		name := base + "-" + strconv.Itoa(n)
		if !plan.Names[name] && !bytes.Contains(source, []byte(name)) {
			return name
		}
	}
}

func lineIndent(source []byte, at int) string {
	start := bytes.LastIndexByte(source[:at], '\n') + 1
	end := start
	for end < at && (source[end] == ' ' || source[end] == '\t') {
		end++
	}
	return string(source[start:end])
}

// insertAfterPlacement inserts text as a new declaration after decl.
func insertAfterPlacement(ctx *Context, decl *gts.Node, text string) ([]byte, error) {
	source := ctx.Source
	nl := Newline(source)
	indent := lineIndent(source, int(decl.StartByte()))
	at := int(decl.EndByte())
	return ReplaceSpan(source, ctx.Options.Path, Span{at, at, nl + indent + text, ctx.Options.Path})
}

func splitClip(ctx *Context, in *SplitClip) ([]byte, string, error) {
	placement, err := resolvePlacement(ctx, in.Entity)
	if err != nil {
		return nil, "", err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, "", err
	}
	var pattern *Pattern
	for i := range plan.Patterns {
		if plan.Patterns[i].ID == placement.Content {
			pattern = &plan.Patterns[i]
		}
	}
	if pattern == nil {
		return nil, "", &RefusalError{Intent: "SplitClip", Cause: "only step pattern placements can be split"}
	}
	if in.AtTick <= placement.AtTick || in.AtTick >= placement.AtTick+placement.LengthTicks {
		return nil, "", &RefusalError{Intent: "SplitClip", Cause: "split point is outside the placement"}
	}
	unit := pattern.LengthTicks()
	if unit <= 0 || (in.AtTick-placement.AtTick)%unit != 0 {
		return nil, "", &RefusalError{Intent: "SplitClip", Cause: fmt.Sprintf("split point is not on a pattern-length boundary (%s is %d ticks)", pattern.ID, unit)}
	}
	bar := in.AtTick/3840 + 1
	// Whether a cut is audible depends on engine state that restarts at a
	// placement boundary (slides, chance iteration, drum voices, and whatever
	// else the engine adds), so no feature list can decide it. Always ask the
	// render; the checks below only word the refusal.
	check, err := renderCheckFor(ctx)
	if err != nil {
		return nil, "", err
	}
	cut, err := splitSource(ctx, plan, placement, in.AtTick)
	if err != nil {
		return nil, "", err
	}
	before, err := check.Hash(ctx.Source)
	if err != nil {
		return nil, "", err
	}
	after, err := check.Hash(cut)
	if err != nil {
		return nil, "", err
	}
	if before != after {
		return nil, "", &RefusalError{Intent: "SplitClip", Cause: splitCause(plan, placement, pattern, bar)}
	}
	return cut, fmt.Sprintf("Clip %s split at %s", placement.ID, FormatPosition(in.AtTick)), nil
}

// splitCause names why a split rendered differently when the pattern shows a
// known reason, and otherwise falls back to the general wording.
func splitCause(plan *Plan, placement Placement, pattern *Pattern, bar int64) string {
	steps := pattern.Data
	for _, lane := range pattern.Lanes {
		steps = append(append([]*Step(nil), steps...), lane...)
	}
	chance := false
	for _, step := range steps {
		chance = chance || step != nil && step.Probability > 0 && step.Probability < 100
	}
	slides := len(pattern.Data) > 0 && pattern.Data[len(pattern.Data)-1] != nil && pattern.Data[len(pattern.Data)-1].Slide
	drums := pattern.Kind == "drums"
	for _, track := range plan.Tracks {
		drums = drums || track.ID == placement.Track && track.Kind == "drums"
	}
	switch {
	case slides:
		return fmt.Sprintf("splitting %s at bar %d cuts a slide", pattern.ID, bar)
	case chance:
		return fmt.Sprintf("splitting %s at bar %d restarts its chance steps", pattern.ID, bar)
	case drums:
		return fmt.Sprintf("splitting %s at bar %d restarts the drum placement", pattern.ID, bar)
	}
	return fmt.Sprintf("splitting %s at bar %d changes the render", pattern.ID, bar)
}

// batchCheck renders candidate sources against the batch's staged state.
type batchCheck struct {
	check RenderCheck
	files map[string][]byte
}

func (b batchCheck) Hash(source []byte) (string, error) {
	if staged, ok := b.check.(StagedRenderCheck); ok {
		return staged.HashWith(source, b.files)
	}
	return b.check.Hash(source)
}

// renderCheckFor returns the configured check bound to every file this batch
// has staged so far, so both renders see exactly what the batch would commit.
func renderCheckFor(ctx *Context) (RenderCheck, error) {
	if ctx.Options.RenderCheck == nil {
		return nil, ErrNoRenderCheck
	}
	return batchCheck{check: ctx.Options.RenderCheck, files: ctx.fileOverrides()}, nil
}

func splitSource(ctx *Context, plan *Plan, placement Placement, atTick int64) ([]byte, error) {
	source := ctx.Source
	decl, _, walker, err := placementDecl(source, placement.ID)
	if err != nil {
		return nil, err
	}
	name := freePlacementName(plan, source, placement.ID)
	copyText, err := placementCopy(source, decl, walker, name, FormatPosition(atTick), FormatLength(placement.AtTick+placement.LengthTicks-atTick))
	if err != nil {
		return nil, err
	}
	// Shorten the original and add the second half in one pass over the
	// original offsets.
	var lengthSpan *Span
	for k := 0; k < decl.NamedChildCount(); k++ {
		param := decl.NamedChild(k)
		if walker.Type(param) == "param_decl" && walker.Text(walker.Field(param, "name")) == "length" {
			value := walker.Field(param, "value")
			lengthSpan = &Span{int(value.StartByte()), int(value.EndByte()), FormatLength(atTick - placement.AtTick), ctx.Options.Path}
		}
	}
	if lengthSpan == nil {
		return nil, fmt.Errorf("placement %q has no length field", placement.ID)
	}
	nl := Newline(source)
	indent := lineIndent(source, int(decl.StartByte()))
	at := int(decl.EndByte())
	return PatchSpans(source, ctx.Options.Path, []Span{*lengthSpan, {at, at, nl + indent + copyText, ctx.Options.Path}})
}

// duplicateClip adds sound by design (a second copy plays after the first), so
// it needs no render check: the music is meant to change.
func duplicateClip(ctx *Context, in *DuplicateClip) ([]byte, string, error) {
	placement, err := resolvePlacement(ctx, in.Entity)
	if err != nil {
		return nil, "", err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, "", err
	}
	decl, _, walker, err := placementDecl(ctx.Source, placement.ID)
	if err != nil {
		return nil, "", err
	}
	name := freePlacementName(plan, ctx.Source, placement.ID)
	text, err := placementCopy(ctx.Source, decl, walker, name, FormatPosition(placement.AtTick+placement.LengthTicks), FormatLength(placement.LengthTicks))
	if err != nil {
		return nil, "", err
	}
	updated, err := insertAfterPlacement(ctx, decl, text)
	return updated, fmt.Sprintf("Clip %s duplicated as %s", placement.ID, name), err
}

// deleteClip removes sound by design, so it needs no render check: the music is
// meant to change.
func deleteClip(ctx *Context, in *DeleteClip) ([]byte, string, error) {
	placement, err := resolvePlacement(ctx, in.Entity)
	if err != nil {
		return nil, "", err
	}
	source := ctx.Source
	decl, arrange, _, err := placementDecl(source, placement.ID)
	if err != nil {
		return nil, "", err
	}
	start, end := int(decl.StartByte()), int(decl.EndByte())
	lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
	alone := len(bytes.TrimSpace(source[lineStart:start])) == 0
	lineEnd := end
	for lineEnd < len(source) && source[lineEnd] != '\n' {
		lineEnd++
	}
	if alone && len(bytes.TrimSpace(source[end:lineEnd])) == 0 {
		start = attachedCommentStart(source, start, int(arrange.StartByte()))
		end = lineEnd
		if end < len(source) {
			end++ // the newline that ended the declaration's line
		}
	}
	updated, err := ReplaceSpan(source, ctx.Options.Path, Span{start, end, "", ctx.Options.Path})
	return updated, fmt.Sprintf("Clip %s deleted", placement.ID), err
}
