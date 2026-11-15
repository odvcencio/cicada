//go:build cicada_noflush

package subnormal

// Enabled is false under the build tag cicada_noflush: Flush and Flush64
// return their argument, so a render shows what the processors compute
// without flushing. make test-flush-identity compares such renders with
// ordinary ones; nothing ships with this tag.
const Enabled = false

// Flush returns x unchanged under cicada_noflush.
func Flush(x float32) float32 { return x }

// Flush64 returns x unchanged under cicada_noflush.
func Flush64(x float64) float64 { return x }
