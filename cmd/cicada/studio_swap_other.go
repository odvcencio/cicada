//go:build !linux

package main

func studioSwap(path, replacement string) error {
	return errStudioSwapUnavailable
}
