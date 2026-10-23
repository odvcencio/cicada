//go:build !tinygo || !cicada_reactor

package seq

func validationError(message string) error { return Error(message) }
