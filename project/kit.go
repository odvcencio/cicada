package project

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

// CompileKit resolves an authored kit to bounded lane bindings. Missing lanes
// remain off. Instrument defaults are lowered once, before audio playback.
func CompileKit(kit Kit, programs map[string]*instrument.Program) (*[drum.LaneCount]engine.KitLaneBinding, error) {
	return CompileKitAtSampleRate(kit, programs, 48_000)
}

// CompileKitAtSampleRate validates graph delays against each lane's fixed
// trigger pitch at the playback or export rate before binding the graph.
func CompileKitAtSampleRate(kit Kit, programs map[string]*instrument.Program, sampleRate int) (*[drum.LaneCount]engine.KitLaneBinding, error) {
	bindings := new([drum.LaneCount]engine.KitLaneBinding)
	if kit.Lanes == nil {
		return nil, fmt.Errorf("kit %s lanes must be explicit", kit.ID)
	}
	for _, source := range sortedKeys(kit.Lanes) {
		target := kit.Lanes[source]
		lane, ok := drumLane(source)
		if !ok {
			return nil, fmt.Errorf("kit %s has unknown lane %s", kit.ID, source)
		}
		if strings.HasPrefix(target, "builtin.") {
			recipe, ok := drumLane(strings.TrimPrefix(target, "builtin."))
			if !ok {
				return nil, fmt.Errorf("kit %s has unknown built-in drum %s", kit.ID, target)
			}
			bindings[lane] = engine.KitLaneBinding{Kind: engine.KitLaneBuiltin, Recipe: recipe}
			continue
		}
		if strings.HasPrefix(target, "model.") {
			profile, ok := modeledkit.ParseProfile(strings.TrimPrefix(target, "model."))
			if !ok {
				return nil, fmt.Errorf("kit %s has unknown modeled drum %s", kit.ID, target)
			}
			bindings[lane] = engine.KitLaneBinding{
				Kind: engine.KitLaneModeled, Model: profile,
				ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -6,
			}
			continue
		}
		program := programs[target]
		if program == nil || program.Mode != "mono" {
			return nil, fmt.Errorf("kit %s has unknown mono instrument %s", kit.ID, target)
		}
		graph, err := instrument.Lower(program, nil)
		if err != nil {
			return nil, fmt.Errorf("kit %s lane %s: %w", kit.ID, source, err)
		}
		if graph.DelaySamples() > 0 {
			if err := validateGraphDelayNote(graph, sampleRate, int(drum.MIDINotes[lane])); err != nil {
				return nil, fmt.Errorf("CICADA-PARAM: kit %s lane %s: %w", kit.ID, source, err)
			}
		}
		bindings[lane] = engine.KitLaneBinding{Kind: engine.KitLaneGraph, Program: graph}
	}
	return bindings, nil
}

// CompileKitTrack applies controls to modeled pieces in an authored kit. The
// lane prefix names the source lane, regardless of its modeled articulation.
// Tune and decay are multipliers; position and humanize are unitless controls.
func CompileKitTrack(kit Kit, programs map[string]*instrument.Program, params map[string]Value) (*[drum.LaneCount]engine.KitLaneBinding, error) {
	return CompileKitTrackAtSampleRate(kit, programs, params, 48_000)
}

// CompileKitTrackAtSampleRate preserves modeled controls and validates graph
// delay bindings at the requested playback or export rate.
func CompileKitTrackAtSampleRate(kit Kit, programs map[string]*instrument.Program, params map[string]Value, sampleRate int) (*[drum.LaneCount]engine.KitLaneBinding, error) {
	bindings, err := CompileKitAtSampleRate(kit, programs, sampleRate)
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(params) {
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("kit %s has unknown parameter %s", kit.ID, name)
		}
		lane, ok := drumLane(parts[0])
		if !ok || bindings[lane].Kind != engine.KitLaneModeled {
			return nil, fmt.Errorf("kit %s parameter %s requires a modeled lane", kit.ID, name)
		}
		value := params[name]
		if value.Number == nil || !finite(*value.Number) || value.Text != "" {
			return nil, fmt.Errorf("kit %s parameter %s requires a finite number", kit.ID, name)
		}
		unit := "unit"
		if parts[1] == "level" {
			unit = "db"
		}
		if value.Unit != unit {
			return nil, fmt.Errorf("kit %s parameter %s requires %s, got %s", kit.ID, name, unit, value.Unit)
		}
		binding := &bindings[lane]
		number := *value.Number
		switch parts[1] {
		case "tune":
			binding.ModelParams.Tune = number
		case "decay":
			binding.ModelParams.Decay = number
		case "position":
			binding.ModelParams.Position = number
		case "humanize":
			binding.ModelParams.Humanize = number
		case "level":
			if number < -60 || number > 6 {
				return nil, fmt.Errorf("kit %s parameter %s requires -60..+6 dB", kit.ID, name)
			}
			binding.ModelLevelDB = number
		case "pan":
			if number < -1 || number > 1 {
				return nil, fmt.Errorf("kit %s parameter %s requires -1..1", kit.ID, name)
			}
			binding.ModelPan = number
		default:
			return nil, fmt.Errorf("kit %s has unknown modeled parameter %s", kit.ID, name)
		}
		if err := binding.ModelParams.Validate(); err != nil {
			return nil, fmt.Errorf("kit %s parameter %s: %w", kit.ID, name, err)
		}
	}
	return bindings, nil
}
