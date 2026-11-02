package main

import "m31labs.dev/cicada/kernel/engine"

var e *engine.Engine

//go:wasmexport modulation_probe_setup
func setup() uint32 {
	var err error
	e, err = engine.ModulationProbeSetup()
	if err != nil {
		return 1
	}
	return 0
}

//go:wasmexport modulation_probe_run
func run(n uint32) { e.ModulationProbeRun(n) }
func main()        {}
