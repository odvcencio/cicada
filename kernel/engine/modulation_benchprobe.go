//go:build mod_benchmark && modular

package engine

// This probe is built only with mod_benchmark. It measures the same advancing
// 32-source, 64-distinct-target grid workload as the native benchmark.

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

func ModulationProbeSetup() (*Engine, error) {
	cfg := Config{SampleRate: 48000, MaxBlock: 4096, Tracks: 16, MaxVoices: 32, BPMMilli: 120000, Seed: 4242}
	cfg.Patterns = make([]PatternBank, 16)
	cfg.Scenes = []Scene{{}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 2}}
	for i := 0; i < 16; i++ {
		cfg.Track[i].Kind = VoiceAcid
	}
	for i := 0; i < MaxModulators; i++ {
		cfg.Modulators = append(cfg.Modulators, ModulatorSpec{Kind: ModKindSway, Octaves: 4, RateTicks: uint32(960 + 7*i)})
	}
	rows := []kernel.ParamID{kernel.ParamMixGain, kernel.ParamMixPan, kernel.ParamMixSendA, kernel.ParamMixSendB}
	for i := 0; i < MaxModRoutes; i++ {
		cfg.Routes = append(cfg.Routes, RouteSpec{Source: uint8(i % MaxModulators), Track: uint8(i / len(rows)), Param: uint16(rows[i%len(rows)]), Depth: .01})
	}
	e, err := New(cfg)
	if err != nil {
		return nil, err
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	left, right := make([]float32, 128), make([]float32, 128)
	e.Render(left, right)
	return e, nil
}
func (e *Engine) ModulationProbeRun(n uint32) {
	for i := uint32(0); i < n; i++ {
		e.transport.Advance(128)
		e.advanceModulation()
	}
}
