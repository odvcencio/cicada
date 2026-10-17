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

func TestPatternEditNowFlagIsAcceptedOnlyOnPatternEdits(t *testing.T) {
	errText := func(c Command) string {
		if err := c.Validate(1); err != nil {
			return err.Error()
		}
		return ""
	}
	for _, op := range []Op{OpSetStep, OpSetPatternLen, OpSetPatternMeta, OpSetChordStep} {
		plain := Command{Op: op, Track: 0, Index: 4}
		flagged, other := plain, plain
		flagged.Pad, other.Pad = PatternEditNow, 2
		if errText(flagged) != errText(plain) {
			t.Fatalf("op %d: the flag changed validation: %q / %q", op, errText(flagged), errText(plain))
		}
		if errText(other) == errText(plain) {
			t.Fatalf("op %d accepted another padding value", op)
		}
	}
	if errText(Command{Op: OpSetParam, Track: 0, Pad: PatternEditNow}) == errText(Command{Op: OpSetParam, Track: 0}) {
		t.Fatal("the flag is only for pattern edits")
	}
}
