package kernelimage_test

import (
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
)

// Image version 19 will carry return slots C to H (engine.Config.Routing). The
// TinyGo engine ignores Routing without an error (kernel/engine/engine.go,
// NewFromConfig, and returns_disabled.go), which is safe only while no image
// can carry it. This test fails as soon as the decoder accepts version 19:
// before that, make the TinyGo engine build the extra returns or refuse a
// config with Routing.
func TestNoImageVersionCarriesRoutingYet(t *testing.T) {
	image, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if version := uint16(image[4]) | uint16(image[5])<<8; version < 12 || version > 18 {
		t.Fatalf("image version %d at bytes 4-5: the header layout this test assumes has changed", version)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Routing != nil {
		t.Fatal("a decoded image carries Routing, which the TinyGo engine ignores")
	}
	image[4], image[5] = 19, 0
	if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
		t.Fatal("the decoder accepts image version 19: the TinyGo engine must build return slots C to H or refuse Routing first (kernel/engine/returns_disabled.go)")
	}
}
