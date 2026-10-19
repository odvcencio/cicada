//go:build !tinygo || !cicada_reactor

package sample

func validationError(message string) error { return Error(message) }
