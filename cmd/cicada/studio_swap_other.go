//go:build !linux && !windows && !darwin

package main

func studioSwap(path, replacement string) (string, error) {
	return "", errStudioSwapUnavailable
}
