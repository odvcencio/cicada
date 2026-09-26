//go:build !linux && !windows

package main

func studioSwap(path, replacement string) (string, error) {
	return "", errStudioSwapUnavailable
}
