package cmd

import (
	"testing"

	"m31labs.dev/cicada/kernel/limits"
)

// The binding names its track in eight bits of Arg0. The bound is the build's
// track capacity (64 native, 16 portable), not a fixed 16.
func TestSceneBindingTrackBoundFollowsTheBuildCapacity(t *testing.T) {
	bind := func(track int) error {
		return Command{Op: OpSetSceneBinding, Track: 0xff, Arg0: uint32(track) | 2<<8 | 3<<16}.Validate(uint8(limits.MaxTracks))
	}
	if err := bind(limits.MaxTracks - 1); err != nil {
		t.Fatalf("last track of this build rejected: %v", err)
	}
	if err := bind(limits.MaxTracks); err == nil {
		t.Fatal("a track beyond this build's capacity was accepted")
	}
	if limits.MaxTracks > 16 {
		if err := bind(16); err != nil {
			t.Fatalf("native track 16 rejected: %v", err)
		}
	}
}
