package kernelimage

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
)

// A portable image holds one reverb return until the image format carries
// slots C to H: Encode refuses a config with extra returns and names them.
func TestEncodeRefusesExtraReverbReturns(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, Tracks: 1, MaxVoices: 1, MaxBlock: 128, ReverbB: &fx.ReverbParams{}}
	cfg.Routing = &engine.RoutingConfig{Returns: []engine.ReturnConfig{{Name: "room", Slot: 2, Reverb: &fx.ReverbParams{}}}}
	_, err := Encode(cfg)
	if err == nil || !strings.Contains(err.Error(), "portable images hold one reverb return") || !strings.Contains(err.Error(), "room") {
		t.Fatalf("Encode: %v", err)
	}
}
