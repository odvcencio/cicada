package project

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// CompileKit resolves an authored kit to bounded lane bindings. Missing lanes
// remain off. Instrument defaults are lowered once, before audio playback.
func CompileKit(kit Kit, programs map[string]*instrument.Program) (*[drum.LaneCount]engine.KitLaneBinding, error) {
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
		program := programs[target]
		if program == nil || program.Mode != "mono" {
			return nil, fmt.Errorf("kit %s has unknown mono instrument %s", kit.ID, target)
		}
		graph, err := instrument.Lower(program, nil)
		if err != nil {
			return nil, fmt.Errorf("kit %s lane %s: %w", kit.ID, source, err)
		}
		bindings[lane] = engine.KitLaneBinding{Kind: engine.KitLaneGraph, Program: graph}
	}
	return bindings, nil
}
