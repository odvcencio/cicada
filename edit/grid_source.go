package edit

import (
	"m31labs.dev/cicada/notation"
)

func gridSourceEdit(handler Handler) Handler {
	return func(ctx *Context, intent Intent) error {
		id, lane, index, shared, scoped, err := gridSourceTarget(intent)
		if err != nil || !scoped {
			if err != nil {
				return err
			}
			return handler(ctx, intent)
		}
		score, ds, err := ctx.ParseProject()
		if err != nil {
			return err
		}
		if score == nil || hasErrors(ds) {
			return handler(ctx, intent)
		}
		var authored *notation.Pattern
		for i := range score.Patterns {
			if score.Patterns[i].Name == id {
				authored = &score.Patterns[i]
				break
			}
		}
		if authored == nil {
			return handler(ctx, intent)
		}
		path := authored.Position.File
		// Definition edits follow the actual shared token. Pattern/detach
		// policies instead expand uses inside the owning pattern buffer.
		if shared == "definition" && index >= 0 {
			tokens := authored.Steps
			if lane != "" {
				for _, row := range authored.Lanes {
					if row.Name == lane {
						tokens = row.Hits
						break
					}
				}
			}
			if index < len(tokens) {
				path = tokens[index].Position.File
			}
		}
		if path == "" {
			path = ctx.Options.Path
		}
		working, before, err := ownedSourceContext(ctx, path, id)
		if err != nil {
			return err
		}
		if err := handler(working, intent); err != nil {
			return err
		}
		ctx.Source = stageProjectSource(ctx, path, before, working.Source)
		ctx.SetLabel(working.label)
		return nil
	}
}

func gridSourceTarget(intent Intent) (id, lane string, index int, shared string, scoped bool, err error) {
	var entity EntityID
	step := true
	index, scoped = -1, true
	switch in := intent.(type) {
	case *ToggleStep:
		entity, shared = in.Entity, in.Shared
	case *SetPitch:
		entity, shared = in.Entity, in.Shared
	case *ToggleModifier:
		entity, shared = in.Entity, in.Shared
	case *CycleStep:
		entity, shared = in.Entity, in.Shared
	case *SetDrumVelocity:
		entity, shared = in.Entity, in.Shared
	case *SetStep:
		entity, shared = in.Entity, in.Shared
	case *SetChord:
		entity, shared = in.Entity, in.Shared
	case *SetPatternSettings:
		entity, step = in.Entity, false
	case *ResizePattern:
		entity, shared, step = in.Entity, in.Shared, false
	case *SetRange:
		entity, lane, index, shared, step = in.Entity, in.Lane, in.First, in.Shared, false
	default:
		scoped = false
		return
	}
	if step {
		id, lane, index, err = stepTarget(entity)
	} else {
		id, err = patternTarget(entity)
	}
	return
}
