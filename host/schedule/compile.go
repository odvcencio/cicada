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
	// packs in the project. Zero selects the default cap: 1 GiB
	// (instrumentpack.DefaultMaxResidentPCM), unless the process changed it with
	// instrumentpack.SetDefaultMaxResidentPCM. The cap is counted from the pack
	// manifests before any audio is read, and a pack that takes the total past
	// it is refused with an *instrumentpack.ResidentPCMError that names the cap
	// and the size. The cap does not apply to CompileResident.
	MaxResidentPCM int64
}

// Compile prepares a project for native playback and offline export. Sampler
// pack PCM does not count against the 64 MiB resident budget. The packs share
// one cap instead, which Compile leaves at the default;
// CompileWithOptions sets it.
//
// Each pack is decoded once per process. A later Compile, from a play reload,
// a Studio request or an audition, reuses the PCM that an earlier one decoded
// (see instrumentpack.Pack.LoadShared), so the process holds one copy of a
// pack however many scores built from it are alive.
func Compile(p *project.Project, root string, rate, block int) (engine.Config, error) {
	return CompileWithOptions(p, root, rate, block, Options{})
}

// CompileWithOptions is Compile with a chosen cap on sampler pack PCM.
func CompileWithOptions(p *project.Project, root string, rate, block int, opts Options) (engine.Config, error) {
	packCap, err := instrumentpack.NewResidentCap(opts.MaxResidentPCM)
	if err != nil {
		return engine.Config{}, err
	}
	return compile(p, root, rate, block, modeNative, packCap)
}

// CompileResident retains the bounded PCM kernel-image/browser path. Native
// playback and offline exports use Compile to page long arrangement clips and
// to hold sampler packs outside the 64 MiB budget. Here the assets and the
// packs share that budget.
func CompileResident(p *project.Project, root string, rate, block int) (engine.Config, error) {
	return compile(p, root, rate, block, modeResident, nil)
}

// Validate applies the rules that CompileWithOptions applies, without reading
// audio. It builds the engine configuration, counts the packs' declared PCM
// against the cap, and checks that each pack manifest matches its pin and each
// pack's audio files are present at their declared sizes. It decodes no PCM,
// loads no asset and leaves the shared pack cache as it was, so it can answer
// on every request (Studio asks on each one) without holding a copy of the
// audio. What it cannot see is damage inside a file: a pack whose audio is
// corrupt, or one that cannot play at the output rate, passes here and is
// refused when it is decoded.
func Validate(p *project.Project, root string, rate, block int, opts Options) error {
	packCap, err := instrumentpack.NewResidentCap(opts.MaxResidentPCM)
	if err != nil {
		return err
	}
	_, err = compile(p, root, rate, block, modeValidate, packCap)
	return err
}

// compileMode says which of the three compiles runs.
type compileMode uint8

const (
	modeNative   compileMode = iota // native playback and export; packs under the cap
	modeResident                    // browser image and WAM2; assets and packs share 64 MiB
	modeValidate                    // modeNative's rules without reading audio
)

// compile builds the engine configuration. On the native path (modeNative and
// modeValidate) each sampler pack counts against packCap, from its manifest,
// before its audio is read; on the resident path packCap is nil and the packs
// count against residentBudget together with the assets. modeValidate reads no
// audio: the configuration it returns holds no PCM and must not be played.
func compile(p *project.Project, root string, rate, block int, mode compileMode, packCap *instrumentpack.ResidentCap) (engine.Config, error) {
	if err := project.ValidateProject(p); err != nil {
		return engine.Config{}, err
	}
	if !p.NeedsSampleEngine() {
		return project.CompileEngine(p, rate, block)
	}
	forceResident := mode == modeResident
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
		if mode == modeValidate {
			// The project parse already hashed the file; there is no PCM to hold.
			assets[i] = engine.AudioAsset{SampleRate: a.RateHz}
			resident += bytes
			continue
		}
		region, err := sampleasset.LoadRegion(root, a, 0, a.Frames, 60, false)
		if err != nil {
			return engine.Config{}, err
		}
		assets[i] = engine.AudioAsset{Left: region.Left, Right: region.Right, SampleRate: region.SampleRate}
		resident += bytes
	}
	var streaming engine.StreamingFactory
	if len(streamAssets) > 0 && mode != modeValidate {
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
	use := func(sampler project.Sampler, prepared *instrumentpack.Prepared) {
		config := prepared.Manifest.Config
		config.Voices = sampler.Voices
		packs[sampler.Name] = &engine.PackFactory{Zones: prepared.Zones, Config: config}
	}
	// On the native path every pack is opened and counted against the cap first,
	// from its manifest. No audio is decoded until the whole project is known to
	// fit, so a project over the cap is refused without allocating any of it.
	type openedPack struct {
		sampler project.Sampler
		pack    *instrumentpack.Pack
	}
	var opened []openedPack
	for _, sampler := range p.Samplers {
		if sampler.Pack == "" {
			continue
		}
		switch mode {
		case modeResident:
			prepared, err := instrumentpack.LoadAtRate(root, sampler.Pack, sampler.SHA256, rate)
			if err != nil {
				return engine.Config{}, err
			}
			// Immutable regions count once even when several zones share an asset.
			resident += prepared.ResidentPCMBytes()
			if resident > residentBudget {
				return engine.Config{}, errResidentBudget
			}
			use(sampler, prepared)
		case modeValidate:
			pack, err := packCap.Open(root, sampler.Pack, sampler.SHA256)
			if err != nil {
				return engine.Config{}, err
			}
			if err = pack.CheckFiles(); err != nil {
				return engine.Config{}, err
			}
		default:
			pack, err := packCap.Open(root, sampler.Pack, sampler.SHA256)
			if err != nil {
				return engine.Config{}, err
			}
			opened = append(opened, openedPack{sampler, pack})
		}
	}
	for _, o := range opened {
		// The PCM is the copy that every compile in the process shares.
		prepared, err := o.pack.LoadShared(rate, packCap.Limit())
		if err != nil {
			return engine.Config{}, err
		}
		use(o.sampler, prepared)
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
