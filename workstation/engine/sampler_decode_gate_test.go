package main

import "testing"

func TestSamplerDecodeGateBoundsNeverSettlingPromise(t *testing.T) {
	var gate samplerDecodeGate
	if !gate.acquire() {
		t.Fatal("first decode refused")
	}
	for i := 0; i < 10000; i++ {
		if gate.acquire() {
			t.Fatal("another decode started while first never settles")
		}
	}
	gate.release()
	if !gate.acquire() {
		t.Fatal("settled decode did not free slot")
	}
	gate.release()
}
