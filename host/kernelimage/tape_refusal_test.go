package kernelimage

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/tape"
)

func TestEncodeRefusesTapeEcho(t *testing.T) {
	params := tape.DefaultParams()
	cfg := engine.Config{SampleRate: 48000, BPMMilli: 120000, Tracks: 1, TapeA: &params}
	_, err := Encode(cfg)
	if err == nil || !strings.Contains(err.Error(), "tape echo") {
		t.Fatalf("want a tape refusal, got %v", err)
	}
}
