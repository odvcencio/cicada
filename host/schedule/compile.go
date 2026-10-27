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

// residentBudget is the resident PCM that sampler assets and unstreamed clips
// may hold together. On the resident path (the browser image and the WAM2
// export) sampler packs count against it too.
const residentBudget = 64 << 20

var errResidentBudget = fmt.Errorf("project audio exceeds the 64 MiB resident PCM budget")

// Options tune a native compile.
type Options struct {
	// MaxResidentPCM caps the decoded float32 PCM, in bytes, of all sampler
	// packs in the project. Zero selects instrumentpack.DefaultMaxResidentPCM
	// (1 GiB). A pack whose PCM takes the total past the cap is refused with an
	// *instrumentpack.ResidentPCMError that names the cap and the size measured.
	// The cap does not apply to CompileResident.
	MaxResidentPCM int64
}

// Compile prepares a project for native playback and offline export. Sampler
// pack PCM does not count against the 64 MiB resident budget. The packs share
// one cap instead, which Compile leaves at instrumentpack.DefaultMaxResidentPCM;
// CompileWithOptions sets it.
func Compile(p *project.Project, root string, rate, block int) (engine.Config, error) {
	return CompileWithOptions(p, root, rate, block, Options{})
}

// CompileWithOptions is Compile with a chosen cap on sampler pack PCM.
func CompileWithOptions(p *project.Project, root string, rate, block int, opts Options) (engine.Config, error) {
	packCap, err := instrumentpack.NewResidentCap(opts.MaxResidentPCM)
	if err != nil {
		return engine.Config{}, err
	}
	return compile(p, root, rate, block, false, packCap)
}

// CompileResident retains the bounded PCM kernel-image/browser path. Native
// playback and offline exports use Compile to page long arrangement clips and
// to hold sampler packs outside the 64 MiB budget. Here the assets and the
// packs share that budget.
func CompileResident(p *project.Project, root string, rate, block int) (engine.Config, error) {
	return compile(p, root, rate, block, true, nil)
}

// compile counts each sampler pack against packCap on the native path. When
// forceResident is set, packCap is nil and the packs count against
// residentBudget together with the assets.
func compile(p *project.Project, root string, rate, block int, forceResident bool, packCap *instrumentpack.ResidentCap) (engine.Config, error) {
	if err := project.ValidateProject(p); err != nil {
		return engine.Config{}, err
	}
	if !p.NeedsSampleEngine() {
		return project.CompileEngine(p, rate, block)
	}
	assets := make([]engine.AudioAsset, len(p.Assets))
	var resident int64
	clipAssets, samplerAssets := map[string]bool{}, map[string]bool{}
	for _, clip := range p.Clips {
		clipAssets[clip.Asset] = true
	}
	for _, sampler := range p.Samplers {
		if sampler.Asset != "" {
			samplerAssets[sampler.Asset] = true
		}
	}
	var streamAssets []project.Asset
	var streamIDs []uint32
	for i, a := range p.Assets {
		bytes := a.Frames * int64(a.Channels) * 4
		if !forceResident && clipAssets[a.Name] && !samplerAssets[a.Name] && (bytes > 1<<20 || a.Frames > sampleasset.FrameLimit(a) || bytes > residentBudget-resident) {
			id := uint32(i + 1)
			assets[i] = engine.AudioAsset{SampleRate: a.RateHz, Frames: a.Frames, StreamID: id}
			streamAssets = append(streamAssets, a)
			streamIDs = append(streamIDs, id)
			continue
		}
		if a.Frames > sampleasset.FrameLimit(a) || bytes <= 0 || bytes > residentBudget-resident {
			return engine.Config{}, errResidentBudget
		}
		region, err := sampleasset.LoadRegion(root, a, 0, a.Frames, 60, false)
		if err != nil {
			return engine.Config{}, err
		}
		assets[i] = engine.AudioAsset{Left: region.Left, Right: region.Right, SampleRate: region.SampleRate}
		resident += bytes
	}
	var streaming engine.StreamingFactory
	if len(streamAssets) > 0 {
		plan, err := sampleasset.PrepareStreamPlan(root, streamAssets, streamIDs)
		if err != nil {
			return engine.Config{}, err
		}
		streaming = plan
	}
	cfg, err := project.CompileEngineWithAssets(p, rate, block, assets)
	if err != nil {
		return cfg, err
	}
	cfg.Streaming = streaming
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
		if forceResident {
			// Immutable regions count once even when several zones share an asset.
			resident += prepared.ResidentPCMBytes()
			if resident > residentBudget {
				return engine.Config{}, errResidentBudget
			}
		} else if err := packCap.Add(sampler.Pack, prepared); err != nil {
			return engine.Config{}, err
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
