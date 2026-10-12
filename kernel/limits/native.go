//go:build !tinygo && !js

package limits

const (
	MaxTracks = NativeMaxTracks
	MaxVoices = NativeMaxVoices
)
