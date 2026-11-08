package kernelimage

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

// A portable image cannot move an authored instrument parameter while it
// plays, so the encoder refuses a config that binds one.
func TestEncodeRefusesLiveInstrumentParams(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, BPMMilli: 120000, Tracks: 1, LiveParams: []engine.LiveParam{{Track: 0, Slot: 0, Node: 3}}}
	if _, err := Encode(cfg); err == nil || !strings.Contains(err.Error(), "authored instrument parameter") {
		t.Fatalf("want a live-parameter refusal, got %v", err)
	}
}
