package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/sample"
)

// A kit allocates the whole voice pool of each distinct sampler bank, not one
// voice per lane, so the engine's voice cap sees what the kit builds.
func TestPrepareDrumKitCountsSamplerPoolOncePerBank(t *testing.T) {
	pcm := make([]float32, 256)
	for i := range pcm {
		pcm[i] = .25
	}
	config := sample.DefaultInstrumentConfig()
	config.Voices = 6
	pack := &PackFactory{Config: config}
	for group, note := range []uint8{38, 37} {
		pack.Zones = append(pack.Zones, sample.Zone{Region: sample.Region{Left: pcm, SampleRate: 48000, RootKey: note, End: len(pcm)}, KeyLow: note, KeyHigh: note, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Group: uint8(group), Count: 1, Gain: 1})
	}
	spec := &TrackConfig{Kit: &[drum.LaneCount]KitLaneBinding{}}
	for lane := range spec.Kit {
		spec.Kit[lane].Kind = KitLaneOff
	}
	spec.Kit[drum.SD] = KitLaneBinding{Kind: KitLaneSampler, Pack: pack, SampleNote: 38}
	spec.Kit[drum.RS] = KitLaneBinding{Kind: KitLaneSampler, Pack: pack, SampleNote: 37}
	spec.Kit[drum.BD] = KitLaneBinding{Kind: KitLaneBuiltin, Recipe: drum.BD}
	_, voices, err := prepareDrumKit(spec, 48000, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if voices != 7 {
		t.Fatalf("voices = %d, want 6 for the shared sampler plus 1 for the builtin lane", voices)
	}
}
