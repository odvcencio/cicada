//go:build tinygo || (js && !cicada_native_limits)

package limits

const (
	MaxTracks = PortableMaxTracks
	MaxVoices = PortableMaxVoices
)
