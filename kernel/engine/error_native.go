//go:build !tinygo || !cicada_reactor

package engine

func engineError(message string) error { return Error(message) }
