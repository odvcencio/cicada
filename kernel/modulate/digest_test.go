package modulate_test

import (
	"encoding/hex"
	"testing"

	"m31labs.dev/cicada/kernel/modulate/testdata/fixture"
)

// The digest covers 4,096 grid values of every source kind. The same value is
// checked on arm64 (the native job), in TinyGo (the modulate_wasm test) and here.
func TestSourceDigest(t *testing.T) {
	got := fixture.Digest()
	if hex.EncodeToString(got[:]) != fixture.ExpectedDigest {
		t.Fatalf("source digest = %x, want %s", got, fixture.ExpectedDigest)
	}
}
