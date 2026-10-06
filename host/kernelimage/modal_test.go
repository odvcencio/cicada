package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/modal"
	"testing"
)

func TestModalCapabilityRoundTripAndRejection(t *testing.T) {
	for profile := modal.Profile(0); profile < modal.ProfileCount; profile++ {
		cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120000, Track: [16]engine.TrackConfig{{Kind: engine.VoiceModal, Modal: profile}}}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(image[30:32]) != kernelimage.ModalCapability {
			t.Fatal("missing capability")
		}
		decoded, err := kernelimage.Decode(image, 48000, 128)
		if err != nil || decoded.Track[0].Kind != engine.VoiceModal || decoded.Track[0].Modal != profile {
			t.Fatalf("round trip %v", err)
		}
		again, err := kernelimage.Encode(decoded)
		if err != nil || !bytes.Equal(image, again) {
			t.Fatal("image bytes changed")
		}
		clear(image[30:32])
		if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
			t.Fatal("modal without capability accepted")
		}
		binary.LittleEndian.PutUint16(image[30:32], 1<<15)
		if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
			t.Fatal("unknown capability accepted")
		}
	}
}
