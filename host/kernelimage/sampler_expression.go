package kernelimage

import "m31labs.dev/cicada/kernel/engine"

// SamplerExpressionFlag selects the modular kernel for expressive live input.
const SamplerExpressionFlag uint32 = 1 << 4

func hasSamplerExpression(cfg *engine.Config) bool {
	for i := 0; i < cfg.Tracks; i++ {
		if cfg.Track[i].Kind == engine.VoiceSample {
			return true
		}
	}
	return false
}
