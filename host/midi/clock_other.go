//go:build !darwin || (!amd64 && !arm64)

package midi

// Native MIDI input is unavailable on these hosts.
func HostTimeNanos() int64 { return 0 }
