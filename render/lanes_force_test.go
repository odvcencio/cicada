//go:build !tinygo && !js

package render

import (
	"os"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
)

// TestMain runs every render test with each segment on every lane when
// CICADA_FORCE_LANES=1, so the race detector sees the lanes on test songs
// too light to start them otherwise:
//
//	CICADA_FORCE_LANES=1 go test -race ./render
func TestMain(m *testing.M) {
	if os.Getenv("CICADA_FORCE_LANES") == "1" {
		renderlanes.ForceEveryLane()
	}
	os.Exit(m.Run())
}
