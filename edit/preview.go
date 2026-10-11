package edit

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
)

// PreviewOp targets indices resolved against the supplied plan. It carries
// no authored indices or entity names for the engine to resolve later.
type PreviewOp struct {
	Kind        string  `json:"kind"`
	Track       int     `json:"track"`
	Index       int     `json:"index"`
	Param       string  `json:"param,omitempty"`
	Value       float64 `json:"value"`
	AtTick      int64   `json:"attick,omitempty"`
	LengthTicks int64   `json:"lengthticks,omitempty"`
}

// Previewer is implemented by intents that can describe engine overrides.
type Previewer interface {
	Preview(plan *Plan, intent Intent) ([]PreviewOp, error)
}

// PreviewOps resolves previewable intents against one compiled plan. Other
// intents still contribute to a proposal's source diff. An error returns no
// operations, so callers cannot apply a partial preview.
func PreviewOps(plan *Plan, intents []Intent) ([]PreviewOp, error) {
	ops := make([]PreviewOp, 0)
	for _, intent := range intents {
		previewer, ok := intent.(Previewer)
		if !ok {
			continue
		}
		resolved, err := previewer.Preview(plan, intent)
		if err != nil {
			return nil, err
		}
		ops = append(ops, resolved...)
	}
	return ops, nil
}

// Preview describes numeric track parameters. Other parameter owners and
// nonnumeric values remain in the source diff until the engine supports them.
func (in *SetParam) Preview(plan *Plan, _ Intent) ([]PreviewOp, error) {
	if in.Entity.Kind() != KindParam {
		return nil, fmt.Errorf("setparam needs a param entity, got %q", in.Entity)
	}
	entity, err := Resolve(plan, in.Entity)
	if err != nil {
		return nil, err
	}
	param, err := plan.ResolveParam(entity.Name)
	if err != nil {
		return nil, err
	}
	// Qualified names may contain dots; resolve the longest track prefix.
	track, field := -1, ""
	for i, candidate := range plan.Tracks {
		if strings.HasPrefix(entity.Name, candidate.ID+".") && (track < 0 || len(candidate.ID)+1 > len(entity.Name)-len(field)) {
			track, field = i, strings.TrimPrefix(entity.Name, candidate.ID+".")
		}
	}
	if track < 0 {
		return nil, nil
	}
	value := param.Descriptor.Default
	if in.Value != nil {
		literal, err := CanonicalMixerLiteral(param.Descriptor, in.Value)
		if err != nil {
			return nil, err
		}
		var numeric bool
		value, _, numeric = paramdefs.LiteralNumber(literal)
		if !numeric {
			return nil, nil
		}
	}
	return []PreviewOp{{Kind: in.Kind(), Track: track, Param: field, Value: value}}, nil
}
