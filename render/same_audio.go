package render

import (
	"math"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// SameAudio reports whether rendering score with a and with b writes the same
// WAV bytes and the same Report apart from Report.MasterGainDB and the
// scheduling counts of Report.LaneReport. It answers
// without rendering: every option must be equal except the export gains
// (MasterGainDB, MasterInputGainDB) and MasterCache, and the export gains must
// turn into the same float32 gain factors on the renderer and the master chain
// that score uses. A render's output depends on the export gains only through
// those factors, which it computes with the same helpers.
//
// Loudness targeting uses it to skip a pass whose gain differs from an earlier
// pass's by less than the factors can show. False means only that the two
// renders may differ.
func SameAudio(score *notation.Score, a, b Options) bool {
	if score == nil || a.NoteTap != nil || b.NoteTap != nil || !sameNonGainOptions(a, b) {
		return false
	}
	for _, opts := range []Options{a, b} {
		for _, db := range []float64{opts.MasterGainDB, opts.MasterInputGainDB} {
			if math.IsNaN(db) || math.IsInf(db, 0) || db < -120 || db > 24 {
				return false
			}
		}
	}
	p, _ := project.FromScore(score)
	if p == nil {
		return false
	}
	authored, hasLevel := authoredMasterLevelDB(p)
	if UsesScheduledEngine(score, a) {
		gainA, gainB := scheduledMasterGainDB(authored, a), scheduledMasterGainDB(authored, b)
		return validMasterGainDB(gainA) && validMasterGainDB(gainB) && math.Float32bits(engine.MasterGain(gainA)) == math.Float32bits(engine.MasterGain(gainB))
	}
	hasChain := p.Master != nil && len(p.Master.Mixer.Inserts) > 0
	gainA, beforeA, afterA := directMasterGains(a, authored, hasLevel, hasChain)
	gainB, beforeB, afterB := directMasterGains(b, authored, hasLevel, hasChain)
	return validMasterGainDB(gainA) && validMasterGainDB(gainB) &&
		math.Float32bits(beforeA) == math.Float32bits(beforeB) && math.Float32bits(afterA) == math.Float32bits(afterB)
}

// validMasterGainDB is the range both renderers accept for a combined master
// gain.
func validMasterGainDB(db float64) bool {
	return !math.IsNaN(db) && !math.IsInf(db, 0) && db >= -120 && db <= 24
}

// sameNonGainOptions compares every Options field except the export gains,
// MasterCache, NoteTap, StemTracks and StemBuses (the last two only choose the
// files Stems writes, so they never change a render's audio).
// TestSameAudioComparesEveryOption fails when a field is added to Options
// without a decision here. Jobs never changes the WAV bytes, but it sets
// Report.Jobs and Report.Lanes, so it is compared.
func sameNonGainOptions(a, b Options) bool {
	sameDither := a.Dither == nil && b.Dither == nil || a.Dither != nil && b.Dither != nil && *a.Dither == *b.Dither
	return a.AssetRoot == b.AssetRoot && a.AssetDir == b.AssetDir && a.SamplerBaseline == b.SamplerBaseline &&
		a.SampleRate == b.SampleRate && a.Bits == b.Bits && a.Bars == b.Bars && a.From == b.From &&
		math.Float64bits(a.TailSec) == math.Float64bits(b.TailSec) && sameDither && a.Normalize == b.Normalize &&
		a.Block == b.Block && math.Float32bits(a.MasterBiasL) == math.Float32bits(b.MasterBiasL) &&
		math.Float32bits(a.MasterBiasR) == math.Float32bits(b.MasterBiasR) && a.MaxResidentPCM == b.MaxResidentPCM &&
		a.Jobs == b.Jobs
}
