// Package schedule prepares assets and the shared immutable event schedule
// outside native, WASM, and offline render callbacks.
package schedule

import (
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

func Compile(p *project.Project, root string, rate, block int) (engine.Config, error) {
	if err := project.ValidateProject(p); err != nil {
		return engine.Config{}, err
	}
	if !p.NeedsSampleEngine() {
		return project.CompileEngine(p, rate, block)
	}
	assets := make([]engine.AudioAsset, len(p.Assets))
	for i, a := range p.Assets {
		region, err := sampleasset.LoadRegion(root, a, 0, a.Frames, 60, false)
		if err != nil {
			return engine.Config{}, err
		}
		assets[i] = engine.AudioAsset{Left: region.Left, Right: region.Right, SampleRate: region.SampleRate}
	}
	return project.CompileEngineWithAssets(p, rate, block, assets)
}
