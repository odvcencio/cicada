//go:build !tinygo || !cicada_reactor

package drum

func validationError(message string) error { return Error(message) }
