//go:build !tinygo || !cicada_reactor

package graph

func validationError(message string) error { return Error(message) }
