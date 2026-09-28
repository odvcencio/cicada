//go:build !linux && !windows

package audiobackend

import "fmt"

func newTymbalBackend() (Backend, error) {
	return nil, fmt.Errorf("Tymbal audio is unavailable on this platform")
}
