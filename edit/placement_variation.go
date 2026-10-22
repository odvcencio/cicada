package edit

import (
	"bytes"
	"fmt"
)

// VaryPlacement copies a placement's compiled pattern into the entry score and
// changes only that placement to use it. Both changes have one history entry.
type VaryPlacement struct {
	Entity EntityID `json:"entity"`
	Name   string   `json:"name"`
}

func (VaryPlacement) Kind() string { return "varyplacement" }

func init() {
	Register("varyplacement", func() Intent { return &VaryPlacement{} })
	Handle("varyplacement", varyPlacement)
}

func varyPlacement(ctx *Context, intent Intent) error {
	in := intent.(*VaryPlacement)
	if !patternName.MatchString(in.Name) || in.Name == "off" || in.Name == "keep" || in.Name == "stop" {
		return fmt.Errorf("choose a lowercase pattern name of at most 64 letters, digits, underscores, or hyphens; off, keep, and stop are scene controls")
	}
	placement, err := resolvePlacement(ctx, in.Entity)
	if err != nil {
		return err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	var parent *Pattern
	for i := range plan.Patterns {
		pattern := &plan.Patterns[i]
		if pattern.ID == in.Name {
			return fmt.Errorf("pattern %q already exists", in.Name)
		}
		if pattern.ID == placement.Content {
			parent = pattern
		}
	}
	for _, clip := range plan.Clips {
		if clip.ID == in.Name {
			return fmt.Errorf("clip %q already exists; choose another pattern name", in.Name)
		}
	}
	if parent == nil {
		return fmt.Errorf("only pattern placements can make a variation; audio clips keep their original recordings")
	}
	if parent.CopySource == nil {
		return fmt.Errorf("compiled pattern copying is unavailable")
	}
	clone, err := parent.CopySource(in.Name)
	if err != nil {
		return err
	}
	declarations, err := textDeclarations([]byte(clone))
	if err != nil || len(declarations) != 1 || !declarationMatches(declarations[0].kind, "pattern") || declarations[0].name != in.Name {
		return fmt.Errorf("pattern copy must contain one matching declaration")
	}
	before, file, err := arrangementFile(ctx)
	if err != nil {
		return err
	}
	check, err := renderCheckFor(ctx)
	if err != nil {
		return err
	}
	// Append the new declaration to the entry so its normal pattern editor can
	// edit it, even when the parent or arrangement belongs to another source.
	newline := Newline(ctx.Source)
	clone = string(bytes.ReplaceAll([]byte(clone), []byte("\r\n"), []byte("\n")))
	clone = string(bytes.ReplaceAll([]byte(clone), []byte("\n"), []byte(newline)))
	copied := append(bytes.Clone(ctx.Source), []byte(newline+newline+clone+newline)...)
	op := arrangementOp{action: "update", name: placement.ID, settings: &PlacementSettings{Content: in.Name}}
	if file == ctx.Options.Path {
		updated, err := arrangementSource(copied, file, op)
		if err != nil {
			return err
		}
		same, err := ctx.compareRenders("VaryPlacement", check, ctx.Source, updated)
		if err != nil {
			return err
		}
		if !same {
			return &RefusalError{Intent: "VaryPlacement", Cause: "making this variation changes the rendered song"}
		}
		ctx.Source, ctx.plan = updated, nil
	} else {
		// Compare in two stages, each changing one owned buffer. This keeps the
		// render snapshots sound: the old arrangement cannot point at a clone
		// missing from its entry, and both comparisons retain input receipts.
		same, err := ctx.compareRenders("VaryPlacement", check, ctx.Source, copied)
		if err != nil {
			return err
		}
		if !same {
			return &RefusalError{Intent: "VaryPlacement", Cause: "adding the independent pattern changes the rendered song"}
		}
		ctx.Source, ctx.plan = copied, nil
		updated, err := arrangementSource(before, file, op)
		if err != nil {
			return err
		}
		same, err = ctx.compareSourceRenders("VaryPlacement", check, file, before, updated)
		if err != nil {
			return err
		}
		if !same {
			return &RefusalError{Intent: "VaryPlacement", Cause: "making this variation changes the rendered song"}
		}
		ctx.Source, ctx.plan = stageProjectSource(ctx, file, before, updated), nil
	}
	ctx.SetLabel(fmt.Sprintf("Placement %s now uses its own pattern %s", placement.ID, in.Name))
	return nil
}
