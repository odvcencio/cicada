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
	validator = preparedDrumParamValidator(v.drums)
	validator.targets = v.drumTargets
	return validator
}

// PrepareParamValidators prepares detached command validation before playback.
// It uses the same kit preparation as Engine, including remapped recipes.
// Callers retain the result, not the temporary kits or any playing engine.
func PrepareParamValidators(cfg *Config) ([16]PreviewParamValidator, error) {
	var validators [16]PreviewParamValidator
	if cfg == nil || cfg.Tracks < 1 || cfg.Tracks > len(validators) {
		return validators, Error("parameter validation track count is out of range")
	}
	for track := 0; track < cfg.Tracks; track++ {
		if cfg.Track[track].Kind != VoiceDrums {
			continue
		}
		kit, _, err := prepareDrumKit(&cfg.Track[track], cfg.SampleRate, cfg.Seed)
		if err != nil {
			return validators, err
		}
		validators[track] = preparedDrumParamValidator(kit)
	}
	return validators, nil
}

func preparedDrumParamValidator(kit *drum.Kit) PreviewParamValidator {
	validator := PreviewParamValidator{drums: true}
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		validator.targets[lane] = kit.Params(lane)
		validator.lanes[lane] = kit.LiveParamValidator(lane)
	}
	return validator
}

// Validate checks command bounds and the prepared recipe using OpSetParam's
// value conversion. Callers must first resolve track voice applicability.
// Errors are static values; this also runs on the render thread.
func (v *PreviewParamValidator) Validate(id kernel.ParamID, value float32) error {
	spec, ok := kernel.Param(id)
	if !ok || !spec.Live {
		return Error("parameter is unknown or not live")
	}
	off := spec.Off && math.IsInf(float64(value), -1)
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) && !off || !off && (value < spec.Min || value > spec.Max) {
		return Error("parameter value is out of range")
	}
	if spec.Curve == "toggle" && value != 0 && value != 1 {
		return Error("toggle parameter must be zero or one")
	}
	lane, control, ok := drumParamControl(id)
	if !ok {
		return nil
	}
	if !v.drums {
		return Error("drum control requires a drum track")
	}
	params := drumParamValue(v.targets[lane], control, value, off)
	return v.lanes[lane].Validate(params)
}

// prepareDrumKit is the single cold preparation path for render voices and
// detached parameter validation. It preserves the effective lane parameters.
func prepareDrumKit(spec *TrackConfig, sampleRate int, seed uint32) (*drum.Kit, int, error) {
	kit, err := drum.New(sampleRate, seed)
	if err != nil {
		return nil, 0, err
	}
	voices := 0
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		if spec.Kit != nil {
			binding := &spec.Kit[lane]
			switch binding.Kind {
			case KitLaneOff:
				err = kit.Disable(lane)
			case KitLaneBuiltin:
				err = kit.SetRecipe(lane, binding.Recipe)
			case KitLaneGraph:
				err = kit.SetGraphFromProgram(lane, &binding.Program)
			case KitLaneModeled:
				err = kit.SetModeled(lane, binding.Model, binding.ModelParams, binding.ModelLevelDB, binding.ModelPan)
			default:
				return nil, 0, Error("unknown kit lane kind")
			}
			if binding.Kind != KitLaneOff {
				voices++
			}
		} else if lane >= drum.LT && spec.Drums[lane] == (drum.Params{}) {
			// Zero params leave added lanes off. Legacy lanes retain their defaults.
			err = kit.Disable(lane)
		} else {
			voices++
			if spec.Drums[lane] != (drum.Params{}) {
				err = kit.SetParams(lane, spec.Drums[lane])
			}
		}
		if err != nil {
			return nil, 0, err
		}
	}
	return kit, voices, nil
}

// CommittedValue reports the prepared kit's effective value in registry units.
// Recipe remapping and modeled level/pan can differ from descriptor defaults.
func (v *PreviewParamValidator) CommittedValue(id kernel.ParamID) (float64, bool) {
	lane, control, ok := drumParamControl(id)
	if !ok || !v.drums {
		return 0, false
	}
	params := v.targets[lane]
	switch control {
	case drumTune:
		return params.Tune, true
	case drumDecay:
		return params.Decay * 1000, true
	case drumLevel:
		if params.LevelDB == -1000 {
			return math.Inf(-1), true
		}
		return params.LevelDB, true
	case drumPan:
		return params.Pan, true
	}
	return 0, false
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
