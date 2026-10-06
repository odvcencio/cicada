package render

import (
	"m31labs.dev/cicada/internal/vendortest"
	"testing"
)

func TestVendoredNativeAndOfflineByteParity(t *testing.T) {
	verifySourceNativeAndOfflineByteParity(t, vendortest.BeforeAndAfter, "libraries-vendored")
}

func TestVendoredRenderAllocationFree(t *testing.T) {
	verifySourceRenderAllocationFree(t, vendortest.BeforeAndAfter, "libraries-vendored")
}

func BenchmarkVendoredVoiceNext(b *testing.B) {
	benchmarkSourceVoiceNext(b, vendortest.BeforeAndAfter)
}
