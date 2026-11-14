//go:build tinygo || (js && !cicada_native_limits)

package limits

import "testing"

func TestSelectedLimits(t *testing.T) {
	if MaxTracks != 16 || MaxVoices != 32 {
		t.Fatalf("portable profile: got %d tracks and %d voices", MaxTracks, MaxVoices)
	}
}
