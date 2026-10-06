package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

func TestModeledKitImageCapabilitiesAndControls(t *testing.T) {
	for profile := modeledkit.Profile(0); profile < modeledkit.ProfileCount; profile++ {
		kit := new([drum.LaneCount]engine.KitLaneBinding)
		params := modeledkit.DefaultParams()
		params.Tune, params.Decay, params.Position, params.Humanize = 1.2, .8, .7, .03
		kit[drum.CB] = engine.KitLaneBinding{Kind: engine.KitLaneModeled, Model: profile, ModelParams: params, ModelLevelDB: -8, ModelPan: -.4}
		cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 2, MaxVoices: 5, BPMMilli: 120000}
		cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceDrums, Kit: kit}
		cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceModal, Modal: modal.Wood}
		encoded, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(encoded[30:32]) != kernelimage.Capabilities {
			t.Fatal("capabilities were not combined")
		}
		decoded, err := kernelimage.Decode(encoded, 48000, 128)
		if err != nil || decoded.Track[0].Kit == nil || decoded.Track[0].Kit[drum.CB] != kit[drum.CB] {
			t.Fatalf("roundtrip: %v", err)
		}
		again, err := kernelimage.Encode(decoded)
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatal("image changed after roundtrip")
		}
		binary.LittleEndian.PutUint16(encoded[30:32], kernelimage.ModalCapability)
		if _, err := kernelimage.Decode(encoded, 48000, 128); err == nil {
			t.Fatal("accepted modeled kit without capability")
		}
		binary.LittleEndian.PutUint16(encoded[30:32], 1<<15)
		if _, err := kernelimage.Decode(encoded, 48000, 128); err == nil {
			t.Fatal("accepted unknown capability")
		}
		for _, corrupt := range []func(*engine.KitLaneBinding){
			func(b *engine.KitLaneBinding) { b.Model = modeledkit.ProfileCount },
			func(b *engine.KitLaneBinding) { b.ModelParams.Tune = math.NaN() },
			func(b *engine.KitLaneBinding) { b.ModelPan = math.Inf(1) },
			func(b *engine.KitLaneBinding) { b.ModelLevelDB = 7 },
		} {
			original := kit[drum.CB]
			corrupt(&kit[drum.CB])
			if _, err := kernelimage.Encode(cfg); err == nil {
				t.Fatal("accepted invalid model controls")
			}
			kit[drum.CB] = original
		}
	}
}

func TestModeledKitImageRejectsTruncatedBinding(t *testing.T) {
	kit := new([drum.LaneCount]engine.KitLaneBinding)
	kit[drum.BD] = engine.KitLaneBinding{Kind: engine.KitLaneModeled, Model: modeledkit.Kick, ModelParams: modeledkit.DefaultParams(), ModelLevelDB: -6}
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceDrums, Kit: kit}
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Every prefix is incomplete, including the profile and each control word.
	for end := 0; end < len(encoded); end++ {
		if _, err := kernelimage.Decode(encoded[:end], 48000, 128); err == nil {
			t.Fatalf("accepted prefix %d", end)
		}
	}
}
