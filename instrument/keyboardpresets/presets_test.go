package keyboardpresets

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"m31labs.dev/cicada/kernel/voice/keyboard"
)

func TestDefaultSpecTableUnchanged(t *testing.T) {
	hash := sha256.New()
	for _, name := range keyboard.Names {
		spec, err := DefaultSpec(name)
		if err != nil {
			t.Fatal(err)
		}
		_ = binary.Write(hash, binary.LittleEndian, spec.Patch)
		_ = binary.Write(hash, binary.LittleEndian, spec.Controls[:])
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != "f00b7923e2695cb8e55930ce22dec2e9f8b095c7f61a19f45887cb81e79a8605" {
		t.Fatalf("preset table changed: %s", got)
	}
}

func TestDefaultSpecRejectsUnknownPatch(t *testing.T) {
	if _, err := DefaultSpec("nope"); err == nil {
		t.Fatal("unknown patch accepted")
	}
}
