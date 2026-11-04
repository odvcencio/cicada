package engine

import (
	"math"
	"unsafe"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/kernel/fx/chorus"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/mix"
)

// PortableEffectBudget is a conservative limit on owned effect working
// storage, including alignment buffers and fixed control state. It excludes
// allocator metadata and immutable shared tables.
const PortableEffectBudget uint64 = 8 << 20

// fixedEffectAllowance is a portable, conservative struct allowance, pinned
// independent of pointer size. Dynamic backing arrays are counted separately.
const fixedEffectAllowance uint64 = 16 << 10

// ValidatePortableEffectBudget rejects over-budget chorus projects before any
// engine effect is constructed. Projects without chorus keep prior behavior.
func ValidatePortableEffectBudget(cfg *Config) error {
	if !chorusEnabled {
		// Non-modular image readers reject the chorus flag and stage marker.
		return nil
	}
	if cfg == nil || cfg.Tracks < 1 || cfg.Tracks > MaxTracks {
		return engineError("invalid portable effect configuration")
	}
	hasChorus := false
	for _, t := range cfg.Track[:cfg.Tracks] {
		for _, s := range t.InsertChain {
			hasChorus = hasChorus || s.Kind == TrackEffectChorus
		}
	}
	if !hasChorus {
		return nil
	}
	if cfg.SampleRate != 44100 && cfg.SampleRate != 48000 && cfg.SampleRate != 96000 {
		return engineError("invalid portable effect sample rate")
	}
	if cfg.MasterProcessor != nil {
		return engineError("portable chorus cannot account for an externally prepared master processor")
	}
	if unsafe.Sizeof(fx.Drive{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(ampcab.Processor{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(chorus.Processor{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(fx.Compressor{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(fx.Reverb{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(fx.Delay{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(fx.Deesser{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(pro.EQ{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(mix.Limiter{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(mix.Delay{}) > uintptr(fixedEffectAllowance) || unsafe.Sizeof(trackEffectChain{}) > uintptr(fixedEffectAllowance) {
		return engineError("portable effect size allowance is too small")
	}
	// Include a default master limiter and per-track effect/control containers.
	// Reserve a further 128 KiB for bounded modulation, route, and control
	// working tables at their maximum sizes.
	total := fixedEffectAllowance*uint64(1+cfg.Tracks) + 128<<10
	rate := uint64(cfg.SampleRate)
	if cfg.DelayA != nil {
		total += fixedEffectAllowance + 16*(4*rate+2)
	}
	if cfg.ReverbB != nil {
		total += fixedEffectAllowance + fx.ReverbBufferBytes(cfg.SampleRate)
	}
	if cfg.CompMusic != nil {
		total += fixedEffectAllowance + 8*(rate/100)
	}
	maxLatency := 0
	for i := 0; i < cfg.Tracks; i++ {
		track := &cfg.Track[i]
		if len(track.InsertChain) > MaxTrackInserts {
			return engineError("track insert chain exceeds eight stages")
		}
		maxLatency = max(maxLatency, trackInsertLatency(track))
		if track.InsertDrive != nil {
			total += fixedEffectAllowance
		}
		for _, stage := range track.InsertChain {
			if err := stage.Validate(cfg.SampleRate); err != nil {
				return err
			}
			total += fixedEffectAllowance
			switch stage.Kind {
			case TrackEffectComp:
				total += 8 * (rate / 100)
			case TrackEffectChorus:
				total += 8 * (uint64(math.Ceil(.028*float64(cfg.SampleRate))) + 2)
			}
		}
	}
	for i := 0; i < cfg.Tracks; i++ {
		if frames := maxLatency - trackInsertLatency(&cfg.Track[i]); frames > 0 {
			total += fixedEffectAllowance + 8*uint64(frames)
		}
	}
	if total > PortableEffectBudget {
		return engineError("portable chorus effect working storage exceeds 8 MiB")
	}
	return nil
}
