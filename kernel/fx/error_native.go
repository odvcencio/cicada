//go:build !tinygo || !cicada_reactor

package fx

func validationError(message string) error { return Error(message) }
