// Package schedule prepares assets and the shared immutable event schedule
// outside native, WASM, and offline render callbacks.
package schedule

import (
	"fmt"
	"m31labs.dev/cicada/host/instrumentpack"
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
	cfg, err := project.CompileEngineWithAssets(p, rate, block, assets)
	if err != nil {
		return cfg, err
	}
	var resident int64
	for _, a := range assets {
		resident += int64(len(a.Left)+len(a.Right)) * 4
	}
	packs := map[string]*engine.PackFactory{}
	for _, sampler := range p.Samplers {
		if sampler.Pack == "" {
			continue
		}
		prepared, err := instrumentpack.LoadAtRate(root, sampler.Pack, sampler.SHA256, rate)
		if err != nil {
			return engine.Config{}, err
		}
		config := prepared.Manifest.Config
		config.Voices = sampler.Voices
		factory := &engine.PackFactory{Zones: prepared.Zones, Config: config}
		// Count immutable regions once even when several zones share an asset.
		seen := map[*float32]bool{}
		for _, z := range prepared.Zones {
			for _, pcm := range [][]float32{z.Region.Left, z.Region.Right} {
				if len(pcm) > 0 && !seen[&pcm[0]] {
					resident += int64(len(pcm)) * 4
					seen[&pcm[0]] = true
				}
			}
		}
		if resident > 64<<20 {
			return engine.Config{}, fmt.Errorf("project audio exceeds the 64 MiB resident PCM budget")
		}
		packs[sampler.Name] = factory
	}
	for ti, track := range p.Tracks {
		if pack := packs[track.Kind]; pack != nil {
			cfg.Track[ti].Kind = engine.VoicePrepared
			cfg.Track[ti].Prepared = pack
			cfg.Track[ti].Sample = nil
		}
	}
	return cfg, nil
}
