package edit

import (
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
	placement, err := resolvePlacement(ctx, in.Entity)
	if err != nil {
		return err
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return err
	}
	found := false
	for _, pattern := range plan.Patterns {
		found = found || pattern.ID == placement.Content
	}
	if !found {
		return fmt.Errorf("only pattern placements can make a variation; audio clips keep their original recordings")
	}
	clone, err := localPatternCopy(ctx, placement.Content, in.Name, placement.Track)
	if err != nil {
		return err
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
	copied := appendPatternCopy(ctx.Source, clone)
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
