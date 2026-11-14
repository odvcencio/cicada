//go:build !tinygo && (!js || cicada_native_limits)

package limits

import "testing"

func TestSelectedLimits(t *testing.T) {
	if MaxTracks != 64 || MaxVoices != 256 {
		t.Fatalf("native profile: got %d tracks and %d voices", MaxTracks, MaxVoices)
	}
}
