package main

import "testing"

func TestSamplerFrameHoldsIndependentPointers(t *testing.T) {
	h := samplerFrameHolds{}
	if !h.press(11, 1) || !h.press(12, 2) || !h.press(13, 1) {
		t.Fatal("independent or same-pad owner rejected")
	}
	if h.release(12, 1) || !h.release(13, 1) || h[11] != 1 || h[12] != 2 {
		t.Fatal("release crossed pointer ownership")
	}
	if !h.release(12, 2) || !h.release(11, 1) || len(h) != 0 {
		t.Fatal("reverse cancellation left a hold")
	}
}
