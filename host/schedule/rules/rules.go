// Package rules holds the checks of a schedule compile that need no audio:
// the project's engine configuration, the resident PCM budget of its assets and
// the manifests of its sampler packs. schedule.Validate runs them on the
// host's files. A host that holds the score in memory, such as a browser tab,
// runs them with its own Packs, because this package links neither the host's
// sample loader (host/sampleasset) nor its pack reader (host/instrumentpack).
package rules

import (
	"fmt"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

// ResidentBudget is the resident PCM, in bytes, that sampler assets and
// unstreamed clips may hold together. On the resident path (the browser image
// and the WAM2 export) sampler packs count against it too.
const ResidentBudget = 64 << 20

// MaxFrames bounds the resident PCM of one prepared asset (64 MiB stereo).
const MaxFrames = 8 << 20

// FrameLimit is the most frames one asset may hold. A mono recording uses the
// same 64 MiB allowance for a longer song; stereo samples keep MaxFrames.
func FrameLimit(asset project.Asset) int64 {
	if asset.Source == "recorded" && asset.Channels == 1 {
		return 16 << 20
	}
	return MaxFrames
}

// ResidentBudgetError names what pushed the project past ResidentBudget and how
// far: name needs bytes on top of used.
func ResidentBudgetError(name string, bytes, used int64) error {
	if used == 0 {
		return fmt.Errorf("project audio exceeds the 64 MiB resident PCM budget: %s needs %s", name, mib(bytes))
	}
	return fmt.Errorf("project audio exceeds the 64 MiB resident PCM budget: %s needs %s on top of %s already resident", name, mib(bytes), mib(used))
}

func mib(bytes int64) string { return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20)) }

// PlaceAsset decides how a compile holds one asset: streamed from its file, or
// resident in memory, where it needs bytes of the budget on top of resident.
// Only a clip's asset may stream (streamable), and it streams when it is large
// (over 1 MiB of PCM) or would not fit. An asset that must be resident and
// does not fit is refused with ResidentBudgetError.
func PlaceAsset(a project.Asset, streamable bool, resident int64) (stream bool, bytes int64, err error) {
	bytes = a.Frames * int64(a.Channels) * 4
	if streamable && (bytes > 1<<20 || a.Frames > FrameLimit(a) || bytes > ResidentBudget-resident) {
		return true, bytes, nil
	}
	if a.Frames > FrameLimit(a) || bytes <= 0 || bytes > ResidentBudget-resident {
		return false, bytes, ResidentBudgetError("asset "+a.Name, bytes, resident)
	}
	return false, bytes, nil
}

// StreamableAssets names the assets that only clips play. A sampler's asset is
// always resident.
func StreamableAssets(p *project.Project) map[string]bool {
	clips, samplers := map[string]bool{}, map[string]bool{}
	for _, clip := range p.Clips {
		clips[clip.Asset] = true
	}
	for _, sampler := range p.Samplers {
		if sampler.Asset != "" {
			samplers[sampler.Asset] = true
		}
	}
	streamable := map[string]bool{}
	for name := range clips {
		if !samplers[name] {
			streamable[name] = true
		}
	}
	return streamable
}

// Packs checks the sampler packs a project names, from their manifests. One
// value counts the PCM of one check against the host's cap, so a caller makes
// a new one for each Validate call, as schedule.Validate does.
type Packs interface {
	// Check reads the manifest of the pack at path, relative to the project's
	// folder, checks it against pin (the manifest's SHA-256, or empty), counts
	// the PCM it decodes to against the host's cap, and confirms that each of
	// its audio files is present at the size the manifest declares. It reads
	// no audio.
	Check(path, pin string) error
}

// Validate applies the rules of a native schedule compile without reading
// audio: the project must validate and compile to an engine configuration at
// rate and block, each asset must fit the resident budget or stream, and each
// sampler pack must pass packs.Check. It returns the first refusal, with the
// error the compile would return.
func Validate(p *project.Project, rate, block int, packs Packs) error {
	if err := project.ValidateProject(p); err != nil {
		return err
	}
	if !p.NeedsSampleEngine() {
		_, err := project.CompileEngine(p, rate, block)
		return err
	}
	streamable := StreamableAssets(p)
	assets := make([]engine.AudioAsset, len(p.Assets))
	var resident int64
	for i, a := range p.Assets {
		stream, bytes, err := PlaceAsset(a, streamable[a.Name], resident)
		if err != nil {
			return err
		}
		if stream {
			assets[i] = engine.AudioAsset{SampleRate: a.RateHz, Frames: a.Frames, StreamID: uint32(i + 1)}
			continue
		}
		// A check holds no PCM: the configuration is built and dropped.
		assets[i] = engine.AudioAsset{SampleRate: a.RateHz}
		resident += bytes
	}
	if _, err := project.CompileEngineWithAssets(p, rate, block, assets); err != nil {
		return err
	}
	for _, sampler := range p.Samplers {
		if sampler.Pack == "" {
			continue
		}
		if err := packs.Check(sampler.Pack, sampler.SHA256); err != nil {
			return err
		}
	}
	return nil
}
