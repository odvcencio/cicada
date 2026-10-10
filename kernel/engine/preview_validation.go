package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// PreviewParamValidator holds detached validation context for prepared drums.
// Other track controls use the registry's float32 bounds directly.
type PreviewParamValidator struct {
	drums   bool
	targets [drum.LaneCount]drum.Params
	lanes   [drum.LaneCount]drum.LiveParamValidator
}

// PreviewParamValidator must be captured before the engine starts rendering.
// Hosts may then validate previews without reading the playing engine.
func (e *Engine) PreviewParamValidator(track int) PreviewParamValidator {
	var validator PreviewParamValidator
	if track < 0 || track >= e.tracks || e.voices[track].drums == nil {
		return validator
	}
	v := &e.voices[track]
	validator.drums, validator.targets = true, v.drumTargets
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		validator.lanes[lane] = v.drums.LiveParamValidator(lane)
	}
	return validator
}

// Validate applies the same value conversion and lane checks as OpSetParam.
// Callers must first check registry range and track voice applicability.
func (v *PreviewParamValidator) Validate(id kernel.ParamID, value float32) error {
	lane, control, ok := drumParamControl(id)
	if !ok {
		return nil
	}
	if !v.drums {
		return Error("drum control requires a drum track")
	}
	off := kernel.Params[id].Off && math.IsInf(float64(value), -1)
	params := drumParamValue(v.targets[lane], control, value, off)
	return v.lanes[lane].Validate(params)
}

func drumParamValue(params drum.Params, control drumParamField, value float32, off bool) drum.Params {
	switch control {
	case drumTune:
		params.Tune = float64(value)
	case drumDecay:
		params.Decay = float64(value) / 1000
	case drumLevel:
		params.LevelDB = float64(value)
		if off {
			params.LevelDB = -1000
		}
	case drumPan:
		params.Pan = float64(value)
	}
	return params
}
