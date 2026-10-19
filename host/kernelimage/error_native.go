//go:build !tinygo || !cicada_reactor

package kernelimage

func imageError(message string) error { return Error(message) }
