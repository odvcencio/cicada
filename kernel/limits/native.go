//go:build !tinygo && (!js || cicada_native_limits)

package limits

// cicada_native_limits is for the standard-Go score engine in the browser.
// The TinyGo audio kernel always uses the portable profile.
const (
	MaxTracks = NativeMaxTracks
	MaxVoices = NativeMaxVoices
)
