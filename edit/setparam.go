package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SetParam sets or resets one parameter. M1 handles instrument parameters
// (param:<track>.<field>); mixer paths arrive with the mixer port.
type SetParam struct {
	Entity         EntityID        `json:"entity"`                   // param:<owner>.<field>
	Value          json.RawMessage `json:"value,omitempty"`          // number or unit literal string; omitted resets to the default
	ConfirmUpgrade bool            `json:"confirmupgrade,omitempty"` // M2: edition-1 scores need confirmation for mixer writes
}

func (SetParam) Kind() string { return "setparam" }

func init() {
	Register("setparam", func() Intent { return &SetParam{} })
	Handle("setparam", setParam)
}

func setParam(ctx *Context, intent Intent) error {
	in := intent.(*SetParam)
	if in.Entity.Kind() != KindParam {
		return fmt.Errorf("setparam needs a param entity, got %q", in.Entity)
	}
	owner, field, ok := strings.Cut(in.Entity.Name(), ".")
	if !ok {
		return fmt.Errorf("parameter path %q must name an owner and field", in.Entity.Name())
	}
	// An empty owner or field falls through to the writer's own errors, which
	// are the texts Studio has always shown.
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return errors.New("score must validate before adding an instrument") // studio_instrument.go text, kept for parity
	}
	value, reset := "", in.Value == nil
	if !reset {
		if err := json.Unmarshal(in.Value, &value); err != nil {
			var number json.Number
			if err := json.Unmarshal(in.Value, &number); err != nil || number == "" {
				return errors.New("parameter value must be a number or compatible unit literal")
			}
			value = number.String()
		}
	}
	updated, err := instrumentParameterSource(ctx.Source, score, owner, field, value, reset)
	if err != nil {
		return err
	}
	ctx.Source = updated
	if reset {
		ctx.SetLabel("Use default " + owner + "." + field)
	} else {
		ctx.SetLabel("Set " + owner + "." + field)
	}
	return nil
}
