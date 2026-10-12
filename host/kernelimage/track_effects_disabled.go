//go:build tinygo && !trackfx

package kernelimage

import "m31labs.dev/cicada/kernel/engine"

const enabledTrackEffectsCapability uint16 = 0

func writeTrackEffects(w *writer, rate int, specs []engine.TrackEffectConfig) error {
	return Error("track inserts require the vocal kernel module")
}
func readTrackEffects(r *reader, rate int) ([]engine.TrackEffectConfig, error) {
	return nil, Error("track inserts require the vocal kernel module")
}
