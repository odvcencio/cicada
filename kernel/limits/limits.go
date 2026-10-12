// Package limits defines bounded runtime capacities without importing the audio
// engine, so notation validation and the command ABI share the same bounds.
package limits

// Native capacities bound the desktop engine. Portable capacities are part of
// the existing browser/WAM image ABI. MaxTracks and MaxVoices select a profile.
const (
	NativeMaxTracks   = 64
	NativeMaxVoices   = 256
	PortableMaxTracks = 16
	PortableMaxVoices = 32
)
