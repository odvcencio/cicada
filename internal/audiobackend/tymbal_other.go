//go:build !linux && !windows && !darwin

package audiobackend

import "fmt"

func newTymbalBackend() (Backend, error) {
	return nil, fmt.Errorf("Tymbal audio is unavailable on this platform")
}
